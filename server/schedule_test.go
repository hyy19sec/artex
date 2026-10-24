package server

import (
	"testing"
	"time"
)

func mustNormalizeSchedule(t *testing.T, sch TaskSchedule) TaskSchedule {
	t.Helper()
	out, err := sch.normalize()
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	return out
}

func TestScheduleActiveAt(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Skipf("timezone unavailable: %v", err)
	}
	sch := mustNormalizeSchedule(t, TaskSchedule{
		Enabled:  true,
		Timezone: "Asia/Shanghai",
		Weekday:  []ScheduleRange{{Start: "12:00", End: "14:00"}, {Start: "18:00", End: "23:00"}},
		Weekend:  []ScheduleRange{{Start: "09:00", End: "23:00"}},
	})
	cases := []struct {
		name   string
		at     time.Time
		active bool
		window string
	}{
		{"周一 11:59 关闭", time.Date(2026, 10, 26, 11, 59, 0, 0, loc), false, ""},
		{"周一 12:00 开始", time.Date(2026, 10, 26, 12, 0, 0, 0, loc), true, "12:00-14:00"},
		{"周一 13:59 仍在窗口", time.Date(2026, 10, 26, 13, 59, 0, 0, loc), true, "12:00-14:00"},
		{"周一 14:00 结束（不含右端点）", time.Date(2026, 10, 26, 14, 0, 0, 0, loc), false, ""},
		{"周一 18:00 第二时段", time.Date(2026, 10, 26, 18, 0, 0, 0, loc), true, "18:00-23:00"},
		{"周一 23:00 结束", time.Date(2026, 10, 26, 23, 0, 0, 0, loc), false, ""},
		{"周六 09:00 非工作日开始", time.Date(2026, 10, 24, 9, 0, 0, 0, loc), true, "09:00-23:00"},
		{"周六 08:59 关闭", time.Date(2026, 10, 24, 8, 59, 0, 0, loc), false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			active, window := sch.activeAt(tc.at)
			if active != tc.active || window != tc.window {
				t.Fatalf("activeAt(%s) = (%v,%q), want (%v,%q)", tc.at, active, window, tc.active, tc.window)
			}
		})
	}
}

func TestNormalizeScheduleRanges(t *testing.T) {
	merged, err := normalizeScheduleRanges([]ScheduleRange{
		{Start: "18:00", End: "23:00"},
		{Start: "12:00", End: "14:00"},
		{Start: "13:00", End: "15:00"},
		{Start: "15:00", End: "16:00"},
	})
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	want := []ScheduleRange{{Start: "12:00", End: "16:00"}, {Start: "18:00", End: "23:00"}}
	if len(merged) != len(want) {
		t.Fatalf("merged = %#v, want %#v", merged, want)
	}
	for i := range want {
		if merged[i] != want[i] {
			t.Fatalf("merged[%d] = %#v, want %#v", i, merged[i], want[i])
		}
	}
	if _, err := normalizeScheduleRanges([]ScheduleRange{{Start: "23:00", End: "09:00"}}); err == nil {
		t.Fatal("cross-midnight range should be rejected")
	}
}

func TestScheduleNextChange(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Skipf("timezone unavailable: %v", err)
	}
	sch := mustNormalizeSchedule(t, TaskSchedule{
		Enabled:  true,
		Timezone: "Asia/Shanghai",
		Weekday:  []ScheduleRange{{Start: "12:00", End: "14:00"}, {Start: "18:00", End: "23:00"}},
		Weekend:  []ScheduleRange{{Start: "09:00", End: "23:00"}},
	})
	cases := []struct {
		name   string
		at     time.Time
		want   time.Time
		action string
	}{
		{"周一 11:00 下次开始", time.Date(2026, 10, 26, 11, 0, 0, 0, loc), time.Date(2026, 10, 26, 12, 0, 0, 0, loc), "resume"},
		{"周一 14:30 下次开始", time.Date(2026, 10, 26, 14, 30, 0, 0, loc), time.Date(2026, 10, 26, 18, 0, 0, 0, loc), "resume"},
		{"周一 23:30 次日开始", time.Date(2026, 10, 26, 23, 30, 0, 0, loc), time.Date(2026, 10, 27, 12, 0, 0, 0, loc), "resume"},
		{"周六 23:30 周日开始", time.Date(2026, 10, 24, 23, 30, 0, 0, loc), time.Date(2026, 10, 25, 9, 0, 0, 0, loc), "resume"},
		{"周日 23:30 周一开始", time.Date(2026, 10, 25, 23, 30, 0, 0, loc), time.Date(2026, 10, 26, 12, 0, 0, 0, loc), "resume"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, action := sch.nextChange(tc.at)
			if !got.Equal(tc.want) || action != tc.action {
				t.Fatalf("nextChange(%s) = (%s,%s), want (%s,%s)", tc.at, got, action, tc.want, tc.action)
			}
		})
	}
}

func TestScheduleHolidayWindows(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Skipf("timezone unavailable: %v", err)
	}
	sch := mustNormalizeSchedule(t, TaskSchedule{
		Enabled:  true,
		Timezone: "Asia/Shanghai",
		Weekday:  []ScheduleRange{{Start: "12:00", End: "14:00"}, {Start: "18:00", End: "23:00"}},
		Weekend:  []ScheduleRange{{Start: "09:00", End: "23:00"}},
	})
	cases := []struct {
		name   string
		at     time.Time
		active bool
		window string
	}{
		{"国庆（周四）全天走非工作日", time.Date(2026, 10, 1, 10, 0, 0, 0, loc), true, "09:00-23:00"},
		{"春节（周二）全天走非工作日", time.Date(2026, 2, 17, 10, 0, 0, 0, loc), true, "09:00-23:00"},
		{"节后第一个工作日 10:00 不在窗口", time.Date(2026, 10, 8, 10, 0, 0, 0, loc), false, ""},
		{"节后第一个工作日 12:00 进入工作日窗口", time.Date(2026, 10, 8, 12, 0, 0, 0, loc), true, "12:00-14:00"},
		{"调休上班的周六仍按非工作日（低价）", time.Date(2026, 10, 10, 10, 0, 0, 0, loc), true, "09:00-23:00"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			active, window := sch.activeAt(tc.at)
			if active != tc.active || window != tc.window {
				t.Fatalf("activeAt(%s) = (%v,%q), want (%v,%q)", tc.at, active, window, tc.active, tc.window)
			}
		})
	}

	// 节前 23:30 → 国庆第一天 09:00 恢复（不是等假期结束）。
	next, action := sch.nextChange(time.Date(2026, 9, 30, 23, 30, 0, 0, loc))
	if want := time.Date(2026, 10, 1, 9, 0, 0, 0, loc); !next.Equal(want) || action != "resume" {
		t.Fatalf("holiday nextChange = (%s,%s), want (%s,resume)", next, action, want)
	}
	// 假期最后一天 23:30 → 节后 12:00 恢复。
	next, action = sch.nextChange(time.Date(2026, 10, 7, 23, 30, 0, 0, loc))
	if want := time.Date(2026, 10, 8, 12, 0, 0, 0, loc); !next.Equal(want) || action != "resume" {
		t.Fatalf("post-holiday nextChange = (%s,%s), want (%s,resume)", next, action, want)
	}

	status := sch.status(time.Date(2026, 10, 1, 10, 0, 0, 0, loc))
	if !status.HolidayCovered || status.HolidayYear != 2026 || status.HolidaySource == "" {
		t.Fatalf("holiday status = %#v", status)
	}
}

func TestHolidayCoverage(t *testing.T) {
	if covered, source := cnHolidayCoverage(2026); !covered || source == "" {
		t.Fatalf("2026 coverage = (%v,%q), want covered with source", covered, source)
	}
	if covered, _ := cnHolidayCoverage(1999); covered {
		t.Fatal("1999 should not be covered")
	}
	if cnHolidayIsWorkday(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatal("2026-10-01 should be off")
	}
	if !cnHolidayIsWorkday(time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)) {
		t.Fatal("2026-10-08 should be a workday")
	}
}

func TestScheduleDisabledStatus(t *testing.T) {
	sch := defaultTaskSchedule()
	status := sch.status(time.Now())
	if status.Enabled || status.Active || status.NextChange != "" {
		t.Fatalf("disabled schedule status = %#v", status)
	}
}
