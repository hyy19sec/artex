package server

import "time"

// 中国法定节假日安排（按年内置）。
//
// 数据来源：国务院办公厅年度节假日安排通知（2026 年通知见 source URL），
// 日期明细参考 NateScarlet/holiday-cn 的公开 JSON（含调休上班日）。
//
// 定时测试的判定口径与 DeepSeek 计费口径保持一致：
//
//	高峰/工作日 = 周一至周五，且不是法定节假日（isOffDay=true）
//	低价/非工作日 = 周末 + 法定节假日（含调休上班的周末；官方口径只按周一至周五算高峰）
//
// 调休上班日（isOffDay=false）仍保留在数据里，便于以后需要“按法定工作日”调度时
// 单独使用；当前版本不把它从周末里抬回工作日。
var cnHolidayCalendars = map[int]HolidayCalendar{
	2026: buildHolidayCalendar(
		2026,
		"国务院办公厅关于2026年部分节假日安排的通知 (https://www.gov.cn/zhengce/zhengceku/202511/content_7047091.htm)",
		[]HolidayDay{
			// 元旦
			{Date: "2026-01-01", Name: "元旦", OffDay: true},
			{Date: "2026-01-02", Name: "元旦", OffDay: true},
			{Date: "2026-01-03", Name: "元旦", OffDay: true},
			{Date: "2026-01-04", Name: "元旦调休上班", OffDay: false},
			// 春节
			{Date: "2026-02-14", Name: "春节调休上班", OffDay: false},
			{Date: "2026-02-15", Name: "春节", OffDay: true},
			{Date: "2026-02-16", Name: "春节", OffDay: true},
			{Date: "2026-02-17", Name: "春节", OffDay: true},
			{Date: "2026-02-18", Name: "春节", OffDay: true},
			{Date: "2026-02-19", Name: "春节", OffDay: true},
			{Date: "2026-02-20", Name: "春节", OffDay: true},
			{Date: "2026-02-21", Name: "春节", OffDay: true},
			{Date: "2026-02-22", Name: "春节", OffDay: true},
			{Date: "2026-02-23", Name: "春节", OffDay: true},
			{Date: "2026-02-28", Name: "春节调休上班", OffDay: false},
			// 清明节
			{Date: "2026-04-04", Name: "清明节", OffDay: true},
			{Date: "2026-04-05", Name: "清明节", OffDay: true},
			{Date: "2026-04-06", Name: "清明节", OffDay: true},
			// 劳动节
			{Date: "2026-05-01", Name: "劳动节", OffDay: true},
			{Date: "2026-05-02", Name: "劳动节", OffDay: true},
			{Date: "2026-05-03", Name: "劳动节", OffDay: true},
			{Date: "2026-05-04", Name: "劳动节", OffDay: true},
			{Date: "2026-05-05", Name: "劳动节", OffDay: true},
			{Date: "2026-05-09", Name: "劳动节调休上班", OffDay: false},
			// 端午节
			{Date: "2026-06-19", Name: "端午节", OffDay: true},
			{Date: "2026-06-20", Name: "端午节", OffDay: true},
			{Date: "2026-06-21", Name: "端午节", OffDay: true},
			// 中秋 / 国庆
			{Date: "2026-09-20", Name: "国庆节调休上班", OffDay: false},
			{Date: "2026-09-25", Name: "中秋节", OffDay: true},
			{Date: "2026-09-26", Name: "中秋节", OffDay: true},
			{Date: "2026-09-27", Name: "中秋节", OffDay: true},
			{Date: "2026-10-01", Name: "国庆节", OffDay: true},
			{Date: "2026-10-02", Name: "国庆节", OffDay: true},
			{Date: "2026-10-03", Name: "国庆节", OffDay: true},
			{Date: "2026-10-04", Name: "国庆节", OffDay: true},
			{Date: "2026-10-05", Name: "国庆节", OffDay: true},
			{Date: "2026-10-06", Name: "国庆节", OffDay: true},
			{Date: "2026-10-07", Name: "国庆节", OffDay: true},
			{Date: "2026-10-10", Name: "国庆节调休上班", OffDay: false},
		},
	),
}

// HolidayDay 是官方节假日安排中的一天。OffDay=false 表示调休上班。
type HolidayDay struct {
	Date   string `json:"date"` // YYYY-MM-DD
	Name   string `json:"name"`
	OffDay bool   `json:"off_day"`
}

// HolidayCalendar 是一年的节假日安排。
type HolidayCalendar struct {
	Year     int
	Source   string
	OffDays  map[string]string // date -> 节日名（放假）
	WorkDays map[string]string // date -> 节日名（调休上班）
}

func buildHolidayCalendar(year int, source string, days []HolidayDay) HolidayCalendar {
	c := HolidayCalendar{
		Year:     year,
		Source:   source,
		OffDays:  make(map[string]string, len(days)),
		WorkDays: make(map[string]string),
	}
	for _, d := range days {
		if d.OffDay {
			c.OffDays[d.Date] = d.Name
		} else {
			c.WorkDays[d.Date] = d.Name
		}
	}
	return c
}

func holidayCalendarFor(year int) (HolidayCalendar, bool) {
	cal, ok := cnHolidayCalendars[year]
	return cal, ok
}

func (c HolidayCalendar) dateKey(t time.Time) string { return t.Format("2006-01-02") }

func (c HolidayCalendar) IsOffDay(t time.Time) bool {
	_, ok := c.OffDays[c.dateKey(t)]
	return ok
}

func (c HolidayCalendar) IsMakeupWorkday(t time.Time) bool {
	_, ok := c.WorkDays[c.dateKey(t)]
	return ok
}

// cnHolidayIsWorkday classifies a date for the testing windows / DeepSeek peak
// pricing: weekday (Mon-Fri) that is not a statutory holiday. Weekends and
// holidays are off; makeup workdays on weekends are intentionally left off
// because the provider's peak definition is "Monday-Friday excluding holidays".
// Years without an embedded calendar fall back to Mon-Fri.
func cnHolidayIsWorkday(t time.Time) bool {
	if wd := t.Weekday(); wd == time.Saturday || wd == time.Sunday {
		return false
	}
	if cal, ok := holidayCalendarFor(t.Year()); ok && cal.IsOffDay(t) {
		return false
	}
	return true
}

// cnHolidayCoverage reports whether the given year has an embedded calendar.
func cnHolidayCoverage(year int) (covered bool, source string) {
	cal, ok := holidayCalendarFor(year)
	if !ok {
		return false, ""
	}
	return true, cal.Source
}
