package model

import (
	"reflect"
	"testing"
	"time"
)

func TestIDs(t *testing.T) {
	ids, err := ParseIDs("440,570;440\n730")
	if err != nil || !reflect.DeepEqual(ids, []uint32{440, 570, 730}) {
		t.Fatal(ids, err)
	}
	for _, s := range []string{"-1", "1.5", "1e3", "4294967296", "hello", "0"} {
		if _, err := ParseIDs(s); err == nil {
			t.Fatal(s)
		}
	}
}
func TestSchedule(t *testing.T) {
	o := Defaults()
	o.ScheduleEnabled = true
	o.ScheduleDays = []int{1}
	o.ScheduleStart = "22:00"
	o.ScheduleEnd = "06:00"
	for _, tc := range []struct {
		s    string
		want bool
	}{{"2026-09-14T23:00:00", true}, {"2026-09-15T05:59:00", true}, {"2026-09-15T06:00:00", false}, {"2026-09-15T23:00:00", false}} {
		now, _ := time.Parse("2006-01-02T15:04:05", tc.s)
		if InSchedule(now, o) != tc.want {
			t.Fatal(tc)
		}
	}
}
func TestAccrueMidnight(t *testing.T) {
	d := NewData()
	from := time.Date(2026, 9, 14, 23, 59, 59, 0, time.FixedZone("MSK", 10800))
	Accrue(d, from, from.Add(2*time.Second), []uint32{1, 2})
	if d.ActiveMS != 2000 || d.GameMS != 4000 || d.Days["2026-09-14"].GameMS != 2000 || d.Days["2026-09-15"].ActiveMS != 1000 || d.Games["1"] != 2000 {
		t.Fatal(d)
	}
}
