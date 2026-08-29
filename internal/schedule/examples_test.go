package schedule

import (
	"path/filepath"
	"testing"
)

func TestShippedScheduleExamplesParse(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "..", "examples", "schedules", "*.md"))
	if err != nil {
		t.Fatalf("glob schedule examples: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("no schedule examples found")
	}

	parsed := make(map[string]Schedule, len(paths))
	for _, path := range paths {
		schedule, err := Parse(path)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		parsed[filepath.Base(path)] = schedule
	}

	digest, ok := parsed["project-digest-every.md"]
	if !ok {
		t.Fatal("project-digest-every.md was not parsed")
	}
	if digest.Agent != "jared" || digest.Project != "launch-kit" || digest.Every != "6h" || digest.Cron != "" {
		t.Fatalf("unexpected interval example: %+v", digest)
	}

	health, ok := parsed["repo-health-daily.md"]
	if !ok {
		t.Fatal("repo-health-daily.md was not parsed")
	}
	if health.Agent != "jared" || health.Project != "launch-kit" || health.Cron != "0 8 * * *" || health.Every != "" {
		t.Fatalf("unexpected cron example: %+v", health)
	}
}
