package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// 定时测试窗口：按「工作日 / 非工作日」配置多个时间段，到点自动暂停任务，
// 下一个允许时段开始自动恢复。工作日 = 周一至周五，非工作日 = 周六/周日。
//
// 配置存在 settings 表（task_schedule），为 JSON；时间使用配置里的时区（默认
// Asia/Shanghai），避免 Docker 容器以 UTC 运行时时区错位。
const (
	settingTaskSchedule       = "task_schedule"
	defaultScheduleTimezone   = "Asia/Shanghai"
	maxScheduleRangesPerType  = 24
	scheduleTickInterval      = 30 * time.Second
	scheduleCheckIntervalNote = "每 30 秒检查一次"
)

// ScheduleRange 是一个半开区间 [Start, End)，格式 HH:MM（24 小时制）。
type ScheduleRange struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

// TaskSchedule 是定时测试窗口配置。Timezone 为空时按 Asia/Shanghai。
type TaskSchedule struct {
	Enabled  bool            `json:"enabled"`
	Timezone string          `json:"timezone,omitempty"`
	Weekday  []ScheduleRange `json:"weekday"` // 周一至周五
	Weekend  []ScheduleRange `json:"weekend"` // 周六、周日

	loc *time.Location `json:"-"`
}

// TaskScheduleStatus 是配置 + 当前生效状态，供设置页展示。
type TaskScheduleStatus struct {
	TaskSchedule
	Active           bool   `json:"active"`
	Current          string `json:"current,omitempty"`            // 当前命中的时段，如 12:00-14:00
	NextChange       string `json:"next_change,omitempty"`        // RFC3339
	NextChangeAction string `json:"next_change_action,omitempty"` // pause | resume
	// 节假日日历覆盖情况：未覆盖的年份只按周一至周五判断。
	HolidayYear    int    `json:"holiday_year,omitempty"`
	HolidayCovered bool   `json:"holiday_covered"`
	HolidaySource  string `json:"holiday_source,omitempty"`
}

func defaultTaskSchedule() TaskSchedule {
	sch, _ := (TaskSchedule{Timezone: defaultScheduleTimezone}).normalize()
	return sch
}

func parseClockMinutes(v string) (int, error) {
	parts := strings.Split(strings.TrimSpace(v), ":")
	if len(parts) != 2 {
		return 0, fmt.Errorf("时间格式应为 HH:MM，收到 %q", v)
	}
	h, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, fmt.Errorf("时间格式应为 HH:MM，收到 %q", v)
	}
	m, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, fmt.Errorf("时间格式应为 HH:MM，收到 %q", v)
	}
	if h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, fmt.Errorf("时间超出范围：%q", v)
	}
	return h*60 + m, nil
}

func formatClockMinutes(v int) string {
	return fmt.Sprintf("%02d:%02d", v/60, v%60)
}

// normalizeScheduleRanges validates, sorts and merges overlapping/adjacent ranges.
func normalizeScheduleRanges(in []ScheduleRange) ([]ScheduleRange, error) {
	if len(in) > maxScheduleRangesPerType {
		return nil, fmt.Errorf("每个日类型最多 %d 个时段", maxScheduleRangesPerType)
	}
	type span struct{ start, end int }
	spans := make([]span, 0, len(in))
	for _, r := range in {
		start, err := parseClockMinutes(r.Start)
		if err != nil {
			return nil, err
		}
		end, err := parseClockMinutes(r.End)
		if err != nil {
			return nil, err
		}
		if start >= end {
			return nil, fmt.Errorf("时段 %s-%s 无效：开始时间必须早于结束时间（不支持跨午夜，请在两天分别配置）", r.Start, r.End)
		}
		spans = append(spans, span{start, end})
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].start < spans[j].start })
	merged := spans[:0]
	for _, sp := range spans {
		if len(merged) > 0 && sp.start <= merged[len(merged)-1].end {
			if sp.end > merged[len(merged)-1].end {
				merged[len(merged)-1].end = sp.end
			}
			continue
		}
		merged = append(merged, sp)
	}
	out := make([]ScheduleRange, len(merged))
	for i, sp := range merged {
		out[i] = ScheduleRange{Start: formatClockMinutes(sp.start), End: formatClockMinutes(sp.end)}
	}
	return out, nil
}

func (sch TaskSchedule) normalize() (TaskSchedule, error) {
	sch.Timezone = strings.TrimSpace(sch.Timezone)
	if sch.Timezone == "" {
		sch.Timezone = defaultScheduleTimezone
	}
	loc, err := time.LoadLocation(sch.Timezone)
	if err != nil {
		// 精简镜像可能没有 tzdata；中国场景下固定 UTC+8 即可，避免保存直接失败。
		log.Printf("[schedule] 无法加载时区 %q，回退到 UTC+8: %v", sch.Timezone, err)
		loc = time.FixedZone("UTC+8", 8*3600)
	}
	sch.loc = loc
	if sch.Weekday, err = normalizeScheduleRanges(sch.Weekday); err != nil {
		return sch, fmt.Errorf("工作日：%w", err)
	}
	if sch.Weekend, err = normalizeScheduleRanges(sch.Weekend); err != nil {
		return sch, fmt.Errorf("非工作日：%w", err)
	}
	if sch.Enabled && len(sch.Weekday)+len(sch.Weekend) == 0 {
		return sch, fmt.Errorf("启用定时测试时至少要配置一个时段")
	}
	return sch, nil
}

func decodeTaskSchedule(raw string) (TaskSchedule, error) {
	var sch TaskSchedule
	if err := json.Unmarshal([]byte(raw), &sch); err != nil {
		return sch, err
	}
	return sch.normalize()
}

func (sch TaskSchedule) location() *time.Location {
	if sch.loc != nil {
		return sch.loc
	}
	loc, err := time.LoadLocation(strings.TrimSpace(sch.Timezone))
	if err != nil {
		log.Printf("[schedule] 无法加载时区 %q，回退到 UTC+8: %v", sch.Timezone, err)
		return time.FixedZone("UTC+8", 8*3600)
	}
	return loc
}

// isWorkday classifies a date as a peak/workday: Monday-Friday excluding the
// officially published statutory holidays. Weekends (including makeup workdays)
// and holidays use the weekend/off-day ranges, matching DeepSeek's peak rule.
func (sch TaskSchedule) isWorkday(t time.Time) bool {
	return cnHolidayIsWorkday(t)
}

// activeAt reports whether now falls in an allowed window, plus the matching
// range ("HH:MM-HH:MM") when it does.
func (sch TaskSchedule) activeAt(now time.Time) (bool, string) {
	t := now.In(sch.location())
	ranges := sch.Weekend
	if sch.isWorkday(t) {
		ranges = sch.Weekday
	}
	mins := t.Hour()*60 + t.Minute()
	for _, r := range ranges {
		start, err1 := parseClockMinutes(r.Start)
		end, err2 := parseClockMinutes(r.End)
		if err1 != nil || err2 != nil {
			continue
		}
		if mins >= start && mins < end {
			return true, r.Start + "-" + r.End
		}
	}
	return false, ""
}

// nextChange returns the next boundary time and the action it triggers.
func (sch TaskSchedule) nextChange(now time.Time) (time.Time, string) {
	if !sch.Enabled {
		return time.Time{}, ""
	}
	loc := sch.location()
	t := now.In(loc)
	var best time.Time
	action := ""
	for day := 0; day < 8; day++ {
		d := t.AddDate(0, 0, day)
		ranges := sch.Weekend
		if sch.isWorkday(d) {
			ranges = sch.Weekday
		}
		for _, r := range ranges {
			start, err1 := parseClockMinutes(r.Start)
			end, err2 := parseClockMinutes(r.End)
			if err1 != nil || err2 != nil {
				continue
			}
			for _, ev := range []struct {
				min int
				act string
			}{{start, "resume"}, {end, "pause"}} {
				cand := time.Date(d.Year(), d.Month(), d.Day(), ev.min/60, ev.min%60, 0, 0, loc)
				if !cand.After(t) {
					continue
				}
				if best.IsZero() || cand.Before(best) {
					best, action = cand, ev.act
				}
			}
		}
	}
	return best, action
}

func (sch TaskSchedule) status(now time.Time) TaskScheduleStatus {
	out := TaskScheduleStatus{TaskSchedule: sch}
	local := now.In(sch.location())
	out.HolidayYear = local.Year()
	out.HolidayCovered, out.HolidaySource = cnHolidayCoverage(local.Year())
	if !sch.Enabled {
		return out
	}
	out.Active, out.Current = sch.activeAt(now)
	if next, action := sch.nextChange(now); !next.IsZero() {
		out.NextChange = next.Format(time.RFC3339)
		out.NextChangeAction = action
	}
	return out
}

// loadTaskScheduleFromDB reads the persisted config. Invalid/missing config
// falls back to a disabled schedule so a bad row can never pause all tasks.
func (s *Server) loadTaskScheduleFromDB() TaskSchedule {
	if s.m == nil || s.m.pg == nil {
		return defaultTaskSchedule()
	}
	raw, ok, err := s.m.pg.GetSetting(settingTaskSchedule)
	if err != nil {
		log.Printf("[schedule] 读取配置失败: %v", err)
		return defaultTaskSchedule()
	}
	if !ok || strings.TrimSpace(raw) == "" {
		return defaultTaskSchedule()
	}
	sch, err := decodeTaskSchedule(raw)
	if err != nil {
		log.Printf("[schedule] 配置无效，定时测试保持关闭: %v", err)
		return defaultTaskSchedule()
	}
	return sch
}

func (s *Server) currentTaskSchedule() TaskSchedule {
	s.scheduleMu.RLock()
	defer s.scheduleMu.RUnlock()
	return s.schedule
}

// setTaskSchedule validates and persists the config, then wakes the loop so the
// change applies immediately (including disabling → auto-resume).
func (s *Server) setTaskSchedule(sch TaskSchedule) error {
	if s.m == nil || s.m.pg == nil {
		return fmt.Errorf("数据库不可用")
	}
	normalized, err := sch.normalize()
	if err != nil {
		return err
	}
	raw, err := json.Marshal(normalized)
	if err != nil {
		return err
	}
	if err := s.m.pg.SetSetting(settingTaskSchedule, string(raw)); err != nil {
		return err
	}
	s.scheduleMu.Lock()
	s.schedule = normalized
	s.scheduleMu.Unlock()
	s.kickTaskSchedule()
	return nil
}

func (s *Server) kickTaskSchedule() {
	if s.scheduleKick == nil {
		return
	}
	select {
	case s.scheduleKick <- struct{}{}:
	default:
	}
}

func (s *Server) startTaskScheduleLoop() {
	if s.m == nil || s.m.pg == nil {
		return
	}
	s.schedule = s.loadTaskScheduleFromDB()
	go func() {
		ticker := time.NewTicker(scheduleTickInterval)
		defer ticker.Stop()
		// Evaluate at startup so a task restored outside the window is paused
		// immediately, not after the first tick.
		s.applyTaskSchedule()
		for {
			select {
			case <-s.ctx.Done():
				return
			case <-ticker.C:
				s.applyTaskSchedule()
			case <-s.scheduleKick:
				s.applyTaskSchedule()
			}
		}
	}()
	if sch := s.currentTaskSchedule(); sch.Enabled {
		year := time.Now().In(sch.location()).Year()
		if covered, _ := cnHolidayCoverage(year); !covered {
			log.Printf("[schedule] 未内置 %d 年法定节假日安排，将仅按周一至周五判断", year)
		}
		log.Printf("[schedule] 定时测试已启用（%s，%s）；工作日 %d 段，非工作日 %d 段",
			sch.Timezone, scheduleCheckIntervalNote, len(sch.Weekday), len(sch.Weekend))
	}
}

// applyTaskSchedule enforces the current window: outside → pause running/queued
// tasks (marked scheduled); inside → resume only tasks paused by the schedule.
func (s *Server) applyTaskSchedule() {
	sch := s.currentTaskSchedule()
	now := time.Now()
	if !sch.Enabled {
		s.resumeScheduledTasks("定时测试已关闭")
		return
	}
	if active, window := sch.activeAt(now); active {
		s.resumeScheduledTasks("进入测试时段 " + window)
		return
	}
	s.pauseTasksForSchedule()
}

func (s *Server) pauseTasksForSchedule() {
	paused := 0
	for _, t := range s.m.List() {
		lc := t.lifecycleSnapshot()
		if lc.Paused || isTerminalStatus(lc.Status) || s.engine.IsDeleting(t.ID) {
			continue
		}
		// Only pause tasks that can consume resources. "created" tasks that are
		// still being decomposed and are not queued should not be touched.
		if lc.Status != "running" && !lc.Queued {
			continue
		}
		if _, err := s.applyScheduledPause(t); err != nil {
			// A concurrent pause/delete is harmless; log only unexpected failures.
			if !strings.Contains(err.Error(), "已经暂停") && !strings.Contains(err.Error(), "unavailable") &&
				!strings.Contains(err.Error(), "正在删除") {
				log.Printf("[schedule] 自动暂停任务 #%s 失败: %v", t.ID, err)
			}
			continue
		}
		paused++
	}
	if paused > 0 {
		log.Printf("[schedule] 当前不在允许时段，已自动暂停 %d 个任务", paused)
	}
}

func (s *Server) resumeScheduledTasks(reason string) {
	resumed := 0
	for _, t := range s.m.List() {
		lc := t.lifecycleSnapshot()
		if !lc.Paused || !lc.ScheduledPause || isTerminalStatus(lc.Status) || s.engine.IsDeleting(t.ID) {
			continue
		}
		if _, err := s.admitPausedTask(t); err != nil {
			log.Printf("[schedule] 自动恢复任务 #%s 失败: %v", t.ID, err)
			continue
		}
		resumed++
	}
	if resumed > 0 {
		log.Printf("[schedule] %s，已自动恢复 %d 个任务", reason, resumed)
	}
}

func (s *Server) getTaskSchedule(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.currentTaskSchedule().status(time.Now()))
}

func (s *Server) putTaskSchedule(w http.ResponseWriter, r *http.Request) {
	var req TaskSchedule
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if err := s.setTaskSchedule(req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, s.currentTaskSchedule().status(time.Now()))
}
