package app

import (
	"time"

	dynamicdomain "gmha/internal/domain/dynamic"
)

// mergeDynamicCollectConfig keeps operator choices from a persisted config
// while adding collectors introduced by a newer Manager release. Built-in
// metadata is refreshed so labels and collector mappings do not remain stale.
func mergeDynamicCollectConfig(saved, defaults dynamicdomain.DynamicCollectConfig) (dynamicdomain.DynamicCollectConfig, bool) {
	index := make(map[string]int, len(saved.Tasks))
	for i := range saved.Tasks {
		index[saved.Tasks[i].Name] = i
	}
	changed := false
	for _, latest := range defaults.Tasks {
		position, ok := index[latest.Name]
		if !ok {
			saved.Tasks = append(saved.Tasks, latest)
			index[latest.Name] = len(saved.Tasks) - 1
			changed = true
			continue
		}
		existing := &saved.Tasks[position]
		if existing.Type == "" {
			existing.Type = latest.Type
			changed = true
		}
		if existing.Category == "" && latest.Category != "" {
			existing.Category = latest.Category
			changed = true
		}
		if existing.IntervalSeconds <= 0 {
			existing.IntervalSeconds = latest.IntervalSeconds
			changed = true
		}
		if existing.TimeoutSeconds <= 0 {
			existing.TimeoutSeconds = latest.TimeoutSeconds
			changed = true
		}
		if existing.Params == nil {
			existing.Params = map[string]string{}
		}
		for key, value := range latest.Params {
			if _, exists := existing.Params[key]; !exists {
				existing.Params[key] = value
				changed = true
			}
		}
		if existing.Labels == nil {
			existing.Labels = map[string]string{}
		}
		for key, value := range latest.Labels {
			if existing.Labels[key] != value {
				existing.Labels[key] = value
				changed = true
			}
		}
	}
	if changed {
		saved.UpdatedAt = time.Now().UTC()
		saved.Version = saved.UpdatedAt.Format("20060102T150405.000000000Z") + "-catalog-merge"
	}
	return saved, changed
}
