package purity_check

import "testing"

func TestCanonicalSiteKeyDropsPathAndNormalizesHost(t *testing.T) {
	if got, want := CanonicalSiteKey(" https://TNTAPI.com/v1/ "), "https://tntapi.com"; got != want {
		t.Fatalf("CanonicalSiteKey() = %q, want %q", got, want)
	}
	if got, want := CanonicalSiteKey("not a url"), "not a url"; got != want {
		t.Fatalf("CanonicalSiteKey fallback = %q, want %q", got, want)
	}
}

func TestNormalizeJobListQuery(t *testing.T) {
	query, err := normalizeJobListQuery(JobListQuery{
		Limit:  999,
		Offset: -4,
		Search: "  https://upstream.example  ",
		Status: string(StatusFailed),
	})
	if err != nil {
		t.Fatalf("normalizeJobListQuery returned error: %v", err)
	}
	if query.Limit != 200 || query.Offset != 0 {
		t.Fatalf("pagination = limit %d, offset %d; want limit 200, offset 0", query.Limit, query.Offset)
	}
	if query.Search != "https://upstream.example" {
		t.Fatalf("search = %q", query.Search)
	}
	if query.Status != string(StatusFailed) {
		t.Fatalf("status = %q", query.Status)
	}
}

func TestNormalizeJobListQueryRejectsUnknownStatus(t *testing.T) {
	if _, err := normalizeJobListQuery(JobListQuery{Status: "unknown"}); err == nil {
		t.Fatal("normalizeJobListQuery accepted an unknown status")
	}
}
