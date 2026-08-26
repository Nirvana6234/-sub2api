package admin

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestParseExcludedUserIDs(t *testing.T) {
	ids, err := parseExcludedUserIDs([]string{" 4,7 ", "7", "12"})
	require.NoError(t, err)
	require.Equal(t, []int64{4, 7, 12}, ids)
}

func TestParseExcludedUserIDsRejectsInvalidValues(t *testing.T) {
	_, err := parseExcludedUserIDs([]string{"1,invalid"})
	require.EqualError(t, err, "Invalid exclude_user_ids value")
}

func TestParseExcludedUserIDsFromQueryAcceptsAxiosArraySyntax(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest("GET", "/api/v1/admin/usage?exclude_user_ids[]=9&exclude_user_ids[]=12", nil)

	ids, err := parseExcludedUserIDsFromQuery(ctx)
	require.NoError(t, err)
	require.Equal(t, []int64{9, 12}, ids)
}
