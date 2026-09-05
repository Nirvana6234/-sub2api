package dashboard

import "time"

const dashboardTimezone = "Asia/Shanghai"

// dashboardLocation is the single reporting clock used by dashboard reads,
// writes, and scheduler recovery. It must not depend on the host or database
// session timezone.
func dashboardLocation() *time.Location {
	loc, err := time.LoadLocation(dashboardTimezone)
	if err != nil {
		return time.FixedZone("CST", 8*60*60)
	}
	return loc
}

func dashboardBusinessDay(now time.Time) time.Time {
	local := now.In(dashboardLocation())
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, dashboardLocation())
}

func dashboardDate(now time.Time) string {
	return dashboardBusinessDay(now).Format("2006-01-02")
}

func dashboardDateValue(date string) (time.Time, error) {
	return time.ParseInLocation("2006-01-02", date, dashboardLocation())
}
