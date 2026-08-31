package httpserver

import (
	"strings"
	"testing"
)

func TestFormatResourceUsageAlertUsesValuesAndTemplate(t *testing.T) {
	message := formatResourceUsageAlert("production", 80, 80, 91.25, 80, "{siteName}|{cpu}|{cpuThreshold}|{memory}|{memoryThreshold}")
	if want := "production|80.0|80.0|91.2|80.0"; message != want {
		t.Fatalf("formatResourceUsageAlert = %q, want %q", message, want)
	}
}

func TestFormatResourceUsageAlertUsesDefaultTemplate(t *testing.T) {
	message := formatResourceUsageAlert("", 81, 80, 79, 80, "")
	if message == "" || !strings.Contains(message, "未知站点") || !strings.Contains(message, "81.0%") || !strings.Contains(message, "79.0%") {
		t.Fatalf("default resource alert missing values: %q", message)
	}
}
