package repository

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"reflect"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type accountIDsPayloadMatcher struct {
	want []int64
}

func (m accountIDsPayloadMatcher) Match(value driver.Value) bool {
	raw, ok := value.([]byte)
	if !ok {
		return false
	}
	var payload struct {
		AccountIDs []int64 `json:"account_ids"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return false
	}
	return reflect.DeepEqual(m.want, payload.AccountIDs)
}

func TestAutoPauseExpiredAccountsEnqueuesAffectedAccounts(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	now := time.Now()
	// 过期自动暂停属于系统自动路径：必须与 schedulable 同条 UPDATE 原子写入
	// source=automatic / reason=expired，并且永不覆盖管理员的 manual 所有权。
	mock.ExpectQuery(`(?s)UPDATE accounts.*RETURNING id`).
		WithArgs(
			now,
			service.SchedulabilitySourceAutomatic,
			service.SchedulabilityReasonExpired,
			service.SchedulabilitySourceManual,
		).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(11)).AddRow(int64(29)))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO scheduler_outbox (event_type, account_id, group_id, payload)")).
		WithArgs(service.SchedulerOutboxEventAccountBulkChanged, nil, nil, accountIDsPayloadMatcher{want: []int64{11, 29}}).
		WillReturnResult(sqlmock.NewResult(1, 1))

	repo := newAccountRepositoryWithSQL(nil, db, nil)
	updated, err := repo.AutoPauseExpiredAccounts(context.Background(), now)

	require.NoError(t, err)
	require.EqualValues(t, 2, updated)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAutoPauseExpiredAccountsSkipsOutboxWithoutChanges(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	now := time.Now()
	mock.ExpectQuery(`(?s)UPDATE accounts.*RETURNING id`).
		WithArgs(
			now,
			service.SchedulabilitySourceAutomatic,
			service.SchedulabilityReasonExpired,
			service.SchedulabilitySourceManual,
		).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))

	repo := newAccountRepositoryWithSQL(nil, db, nil)
	updated, err := repo.AutoPauseExpiredAccounts(context.Background(), now)

	require.NoError(t, err)
	require.Zero(t, updated)
	require.NoError(t, mock.ExpectationsWereMet())
}
