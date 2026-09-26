package config

import (
	"fmt"
	"strings"
)

// CPASchedulingConfig grants a CPA instance access to the read-only scheduling
// export. This token grants no administrator or inference API privileges.
type CPASchedulingConfig struct {
	SyncToken string `mapstructure:"sync_token" json:"-" yaml:"-"`
	SourceID  string `mapstructure:"source_id" json:"source_id"`
}

func (c CPASchedulingConfig) Validate() error {
	if c.SyncToken == "" {
		return nil
	}
	if len(c.SyncToken) < 32 || strings.TrimSpace(c.SyncToken) != c.SyncToken {
		return fmt.Errorf("cpa_scheduling.sync_token must contain at least 32 characters without surrounding whitespace")
	}
	if strings.TrimSpace(c.SourceID) == "" || len(c.SourceID) > 128 || strings.TrimSpace(c.SourceID) != c.SourceID {
		return fmt.Errorf("cpa_scheduling.source_id must be a stable nonempty identifier of at most 128 characters")
	}
	return nil
}
