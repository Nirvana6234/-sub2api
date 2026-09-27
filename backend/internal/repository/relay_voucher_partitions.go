package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// 已入账凭证表（relay_voucher_consumed）按签发时间按月分区（迁移 259）：迁移只建默认分区，保证插入永远不会
// 因为缺分区失败；主从分流打开后由这里预建之后几个月的分区、删掉过期的（设计 5.4：保留 90 天，
// 比 60 天的入账期限长，去重记录一定比可入账的凭证活得久）。
//
// 某个月的行已经落进默认分区后就不能再给这个月建分区，所以只预建还没开始的月份。

const (
	relayVoucherRetention   = 90 * 24 * time.Hour
	relayVoucherMonthsAhead = 2
)

// RelayVoucherPartitions 维护已入账凭证表的分区。
type RelayVoucherPartitions struct {
	db *sql.DB
}

// NewRelayVoucherPartitions 创建分区维护。
func NewRelayVoucherPartitions(db *sql.DB) *RelayVoucherPartitions {
	return &RelayVoucherPartitions{db: db}
}

func relayVoucherPartitionName(month time.Time) string {
	return fmt.Sprintf("relay_voucher_consumed_%04d%02d", month.Year(), int(month.Month()))
}

func monthStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// Maintain 预建之后几个月的分区，删掉整段都超过保留期的分区，清掉默认分区里超过保留期的行。
func (p *RelayVoucherPartitions) Maintain(ctx context.Context, now time.Time) error {
	next := monthStart(now).AddDate(0, 1, 0)
	for i := 0; i < relayVoucherMonthsAhead; i++ {
		from := next.AddDate(0, i, 0)
		to := from.AddDate(0, 1, 0)
		name := relayVoucherPartitionName(from)
		if _, err := p.db.ExecContext(ctx, fmt.Sprintf(
			`CREATE TABLE IF NOT EXISTS %s PARTITION OF relay_voucher_consumed FOR VALUES FROM ('%s') TO ('%s')`,
			name, from.Format(time.RFC3339), to.Format(time.RFC3339))); err != nil {
			return fmt.Errorf("create %s: %w", name, err)
		}
	}

	cutoff := now.UTC().Add(-relayVoucherRetention)
	rows, err := p.db.QueryContext(ctx, `SELECT c.relname FROM pg_inherits i
		JOIN pg_class c ON c.oid = i.inhrelid
		JOIN pg_class parent ON parent.oid = i.inhparent
		WHERE parent.relname = 'relay_voucher_consumed' AND c.relname LIKE 'relay_voucher_consumed_2%'`)
	if err != nil {
		return err
	}
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			_ = rows.Close()
			return err
		}
		names = append(names, name)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, name := range names {
		month, err := time.Parse("200601", strings.TrimPrefix(name, "relay_voucher_consumed_"))
		if err != nil {
			continue
		}
		if !month.AddDate(0, 1, 0).After(cutoff) { // 整个月都在保留期之前
			if _, err := p.db.ExecContext(ctx, `DROP TABLE IF EXISTS `+name); err != nil {
				return fmt.Errorf("drop %s: %w", name, err)
			}
		}
	}
	if _, err := p.db.ExecContext(ctx, `DELETE FROM relay_voucher_consumed_default WHERE issued_at < $1`, cutoff); err != nil {
		return fmt.Errorf("purge default voucher partition: %w", err)
	}
	return nil
}
