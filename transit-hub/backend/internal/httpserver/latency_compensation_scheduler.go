package httpserver

import (
	"context"
	"log"
	"time"

	"transithub/backend/internal/modules/my_sites"
)

// latencySubsidyScheduler 每分钟检查一次，把每个开了自动执行的延迟补贴任务
// 在到点后跑一遍（对"当天 00:00 到现在"这段窗口预览+发放）。
//
// 和每日简报调度器故意不同：简报错过就跳过当天，因为补发意味着同一条消息
// 可能被重复推送到机器人；延迟补贴的发放动作在 Sub2API 那边是幂等的（按
// latency_compensated_at 去重，同一批请求只会被补偿一次），所以这里反而
// 应该"补发"——只要当天配置的时刻已过、且今天这个任务还没自动成功执行过，
// 就一直尝试，而不是错过精确的那一分钟（比如进程当时正在重启）就让用户
// 当天白等。是否"今天已经跑过"用数据库里的 last_auto_run_date 持久化，
// 不是内存状态，进程重启不会丢。
type latencySubsidyScheduler struct {
	tasks   my_sites.LatencySubsidyTaskRepository
	mySites *my_sites.Service
}

func newLatencySubsidyScheduler(tasks my_sites.LatencySubsidyTaskRepository, mySitesSvc *my_sites.Service) *latencySubsidyScheduler {
	return &latencySubsidyScheduler{tasks: tasks, mySites: mySitesSvc}
}

// Start 起后台协程，随 ctx 一起结束。
func (s *latencySubsidyScheduler) Start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.tickSafely(ctx)
			}
		}
	}()
}

func (s *latencySubsidyScheduler) tickSafely(ctx context.Context) {
	defer func() {
		if recovered := recover(); recovered != nil {
			log.Printf("[latency-subsidy] 调度 panic 已恢复: %v", recovered)
		}
	}()
	s.tick(ctx)
}

func (s *latencySubsidyScheduler) tick(ctx context.Context) {
	tasks, err := s.tasks.ListAutoEnabledLatencySubsidyTasks(ctx)
	if err != nil {
		log.Printf("[latency-subsidy] 读取自动任务列表失败: %v", err)
		return
	}

	now := time.Now().In(dailyReportLocation())
	currentDay := now.Format("2006-01-02")
	currentTime := now.Format("15:04")

	for _, task := range tasks {
		if task.LastAutoRunDate == currentDay {
			continue
		}
		// 补发语义：只要求"已过窗口结束时刻"，不要求分钟级精确命中当前 tick。
		// window_end 同时是这个任务每天的触发时刻——早于它窗口的数据还不完整。
		if currentTime < task.WindowEnd {
			continue
		}
		if !latencySubsidyTaskDueOn(task, now) {
			continue
		}

		from, err := dailyClockTime(now, task.WindowStart)
		if err != nil {
			log.Printf("[latency-subsidy] 任务窗口起点解析失败 task_id=%s window_start=%q err=%v", task.ID, task.WindowStart, err)
			continue
		}
		to, err := dailyClockTime(now, task.WindowEnd)
		if err != nil {
			log.Printf("[latency-subsidy] 任务窗口终点解析失败 task_id=%s window_end=%q err=%v", task.ID, task.WindowEnd, err)
			continue
		}
		summary, err := s.mySites.RunLatencySubsidyTaskApply(ctx, task.UserID, task.ID, from, to)
		if err != nil {
			log.Printf("[latency-subsidy] 自动发放失败 task_id=%s task_name=%q user_id=%s date=%s err=%v",
				task.ID, task.Name, task.UserID, currentDay, err)
			// 失败不标记 last_auto_run_date，下一分钟继续重试。
			continue
		}
		if markErr := s.tasks.MarkLatencySubsidyTaskAutoRun(ctx, task.ID, currentDay); markErr != nil {
			log.Printf("[latency-subsidy] 标记任务已执行失败（发放本身已成功） task_id=%s date=%s err=%v",
				task.ID, currentDay, markErr)
		}
		log.Printf("[latency-subsidy] 自动发放完成 task_id=%s task_name=%q user_id=%s date=%s 用户数=%d 总额=$%.4f",
			task.ID, task.Name, task.UserID, currentDay, len(summary.Users), summary.TotalCompensation)
	}
}

// latencySubsidyTaskDueOn 判断 now 所在这一天，任务是否应该自动执行——先看
// 有效期(valid_from/valid_until，日期精度，越界直接跳过)，再看周期
// (daily 每天/weekday 周一到周五/weekly 命中 recurrence_days_of_week 才算)。
// now 已经在调用方转换到 Asia/Shanghai。
func latencySubsidyTaskDueOn(task my_sites.LatencySubsidyTask, now time.Time) bool {
	today := now.Format("2006-01-02")
	if task.ValidFrom != nil && today < task.ValidFrom.Format("2006-01-02") {
		return false
	}
	if task.ValidUntil != nil && today > task.ValidUntil.Format("2006-01-02") {
		return false
	}
	switch task.RecurrenceType {
	case my_sites.LatencySubsidyRecurrenceWeekday:
		wd := now.Weekday()
		return wd >= time.Monday && wd <= time.Friday
	case my_sites.LatencySubsidyRecurrenceWeekly:
		wd := int(now.Weekday())
		for _, d := range task.RecurrenceDaysOfWeek {
			if d == wd {
				return true
			}
		}
		return false
	default: // "daily"，以及创建于这个功能之前、字段为空的老任务
		return true
	}
}

// dailyClockTime 把 "HH:MM" 映射到 reference 所在自然日的那个时刻，跟
// reference 用同一个 time.Location（调度器传进来的 now 已经在 Asia/Shanghai）。
func dailyClockTime(reference time.Time, clock string) (time.Time, error) {
	parsed, err := time.Parse("15:04", clock)
	if err != nil {
		return time.Time{}, err
	}
	return time.Date(reference.Year(), reference.Month(), reference.Day(),
		parsed.Hour(), parsed.Minute(), 0, 0, reference.Location()), nil
}
