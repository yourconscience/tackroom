package app

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The words both cron views draw: health, a short name and the schedule in
// words, worked out once here so the web view and the Herdr panel cannot
// disagree (docs/herdr.md).

var (
	cronPausedStates = map[string]bool{"disabled": true, "paused": true, "not_loaded": true}
	cronOKStatuses   = map[string]bool{"ok": true, "success": true, "succeeded": true, "completed": true, "done": true, "cancelled": true, "skipped": true}
	cronLabelRoots   = map[string]bool{"com": true, "org": true, "net": true, "io": true, "ai": true, "sh": true, "dev": true, "me": true, "app": true, "co": true}
	cronDayNames     = []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}
	cronEverySeconds = regexp.MustCompile(`^every (\d+)s$`)
	cronMinuteStep   = regexp.MustCompile(`^\*/(\d+)$`)
	cronDigits       = regexp.MustCompile(`^\d+$`)
	cronFixedOffset  = regexp.MustCompile(`^([+-])(\d\d):(\d\d)$`)
)

// annotateCronJobs fills Health, ShortName and ScheduleText on every job.
func annotateCronJobs(jobs []cronJob, now time.Time) {
	for i := range jobs {
		jobs[i].Health = cronHealth(jobs[i])
		jobs[i].ShortName = cronShortName(jobs[i])
		jobs[i].ScheduleText = cronScheduleText(jobs[i], now)
	}
}

// cronHealth is paused, fail (problems, or dead or missing), ok or warn by
// the last result, or idle with no result yet. A paused job's old error is
// not actionable until it is turned on again.
func cronHealth(job cronJob) string {
	switch {
	case cronPausedStates[job.State]:
		return "paused"
	case len(job.Problems) > 0 || job.State == "dead" || job.State == "missing":
		return "fail"
	case job.LastExit != nil:
		if *job.LastExit == 0 {
			return "ok"
		}
		return "warn"
	case job.LastStatus != "":
		if cronOKStatuses[job.LastStatus] {
			return "ok"
		}
		return "warn"
	case job.State == "service":
		return "ok"
	}
	return "idle"
}

// cronShortName drops a reverse-DNS prefix from a launchd label:
// com.vendor.name reads as name.
func cronShortName(job cronJob) string {
	name := job.Name
	if name == "" {
		name = job.ID
	}
	parts := strings.Split(name, ".")
	if job.Source != "launchd" || len(parts) < 2 || !cronLabelRoots[parts[0]] {
		return name
	}
	if len(parts) > 2 {
		return strings.Join(parts[2:], ".")
	}
	return strings.Join(parts[1:], ".")
}

// cronScheduleText puts a schedule in words. An owned job shows the
// expression it declares; launchd may have expanded it into one entry per
// day ("0 8 */2 * *" into 16), which reads the same here. Anything it does
// not read stays as written.
func cronScheduleText(job cronJob, now time.Time) string {
	if job.State == "service" {
		return "Always on"
	}
	schedule := job.DeclaredSchedule
	if schedule == "" {
		schedule = job.Schedule
	}
	if m := cronEverySeconds.FindStringSubmatch(schedule); m != nil {
		seconds, _ := strconv.Atoi(m[1])
		return "Every " + cronDuration(seconds)
	}
	var lines [][]string
	for _, line := range strings.Split(schedule, ";") {
		fields := strings.Fields(line)
		if len(fields) != 5 {
			return schedule
		}
		lines = append(lines, fields)
	}
	if len(lines) == 0 {
		return schedule
	}
	zone := ""
	if job.TZ != "" && cronZoneOffset(job.TZ, now) != cronZoneOffset("", now) {
		zone = " (" + job.TZ + ")"
	}
	first := lines[0]
	if step := cronMinuteStep.FindStringSubmatch(first[0]); len(lines) == 1 && step != nil && allStars(first[1:]) {
		return "Every " + step[1] + " min"
	}
	if everyLine(lines, func(f []string) bool { return cronDigits.MatchString(f[0]) && allStars(f[1:]) }) {
		minutes := make([]int, 0, len(lines))
		for _, f := range lines {
			minute, _ := strconv.Atoi(f[0])
			minutes = append(minutes, minute)
		}
		sort.Ints(minutes)
		if len(minutes) > 1 && 60%len(minutes) == 0 && evenlySpaced(minutes, 60/len(minutes)) {
			return fmt.Sprintf("Every %d min", 60/len(minutes))
		}
		marks := make([]string, len(minutes))
		for i, minute := range minutes {
			marks[i] = fmt.Sprintf(":%02d", minute)
		}
		return "Hourly at " + strings.Join(marks, ", ")
	}
	m, hr := first[0], first[1]
	if !cronDigits.MatchString(m) || !cronDigits.MatchString(hr) || !everyLine(lines, func(f []string) bool { return f[0] == m && f[1] == hr && f[3] == "*" }) {
		return schedule
	}
	minute, _ := strconv.Atoi(m)
	hour, _ := strconv.Atoi(hr)
	clock := fmt.Sprintf("%02d:%02d", hour, minute)
	if everyLine(lines, func(f []string) bool { return f[2] != "*" && f[4] == "*" }) {
		dates, ok := cronFieldUnion(lines, 2, 1, 31)
		if !ok {
			return schedule
		}
		gap := 0
		if len(dates) > 1 {
			gap = dates[1] - dates[0]
		}
		if dates[0] == 1 && gap > 1 && evenlySpaced(dates, gap) && dates[len(dates)-1]+gap > 31 {
			return fmt.Sprintf("Every %d days %s%s", gap, clock, zone)
		}
		label := "Days"
		if len(dates) == 1 {
			label = "Day"
		}
		return fmt.Sprintf("%s %s of the month, %s%s", label, joinInts(dates), clock, zone)
	}
	if !everyLine(lines, func(f []string) bool { return f[2] == "*" }) {
		return schedule
	}
	days, ok := cronFieldUnion(lines, 4, 0, 7)
	if !ok {
		return schedule
	}
	switch {
	case len(days) == 7:
		return "Daily " + clock + zone
	case len(days) == 5 && days[0] == 1 && days[4] == 5:
		return "Weekdays " + clock + zone
	}
	// Monday first, Sunday last.
	sort.Slice(days, func(i, j int) bool { return (days[i]+6)%7 < (days[j]+6)%7 })
	names := make([]string, len(days))
	for i, day := range days {
		names[i] = cronDayNames[day]
	}
	return strings.Join(names, ", ") + " " + clock + zone
}

func cronDuration(seconds int) string {
	switch {
	case seconds%3600 == 0 && seconds == 3600:
		return "hour"
	case seconds%3600 == 0:
		return fmt.Sprintf("%d h", seconds/3600)
	case seconds%60 == 0 && seconds == 60:
		return "minute"
	case seconds%60 == 0:
		return fmt.Sprintf("%d min", seconds/60)
	}
	return fmt.Sprintf("%d s", seconds)
}

// cronFieldUnion parses field index of every line as a set of numbers and
// returns their sorted union; 7 in a day-of-week field is Sunday (0).
func cronFieldUnion(lines [][]string, index, lo, hi int) ([]int, bool) {
	seen := map[int]bool{}
	for _, fields := range lines {
		for _, part := range strings.Split(fields[index], ",") {
			rng, stepText, hasStep := strings.Cut(part, "/")
			step := 1
			if hasStep {
				n, err := strconv.Atoi(stepText)
				if err != nil || n < 1 {
					return nil, false
				}
				step = n
			}
			from, to := lo, hi
			if rng != "*" {
				a, b, isRange := strings.Cut(rng, "-")
				x, err := strconv.Atoi(a)
				if err != nil {
					return nil, false
				}
				from, to = x, x
				if isRange {
					y, err := strconv.Atoi(b)
					if err != nil {
						return nil, false
					}
					to = y
				} else if hasStep {
					to = hi
				}
			}
			if from < lo || to > hi {
				return nil, false
			}
			for v := from; v <= to; v += step {
				if hi == 7 && v == 7 {
					seen[0] = true
					continue
				}
				seen[v] = true
			}
		}
	}
	out := make([]int, 0, len(seen))
	for v := range seen {
		out = append(out, v)
	}
	sort.Ints(out)
	return out, len(out) > 0
}

// cronZoneOffset is the zone's offset from UTC in minutes at now: an IANA
// name, a +HH:MM offset, or "" for this machine's zone. An unknown zone
// counts as this machine's.
func cronZoneOffset(tz string, now time.Time) int {
	if m := cronFixedOffset.FindStringSubmatch(tz); m != nil {
		h, _ := strconv.Atoi(m[2])
		mins, _ := strconv.Atoi(m[3])
		offset := h*60 + mins
		if m[1] == "-" {
			offset = -offset
		}
		return offset
	}
	loc := time.Local
	if tz != "" {
		if l, err := time.LoadLocation(tz); err == nil {
			loc = l
		}
	}
	_, seconds := now.In(loc).Zone()
	return seconds / 60
}

func allStars(fields []string) bool {
	for _, f := range fields {
		if f != "*" {
			return false
		}
	}
	return true
}

func everyLine(lines [][]string, ok func([]string) bool) bool {
	for _, f := range lines {
		if !ok(f) {
			return false
		}
	}
	return true
}

func evenlySpaced(values []int, gap int) bool {
	for i, v := range values {
		if v != values[0]+i*gap {
			return false
		}
	}
	return true
}

func joinInts(values []int) string {
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = strconv.Itoa(v)
	}
	return strings.Join(parts, ", ")
}
