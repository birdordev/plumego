package kubernetes

import "encoding/json"

// Job 返回数量，CronJob 返回正在运行的 Job 引用。
type activeCount struct {
	number     *int
	references *int
}

func (a *activeCount) UnmarshalJSON(data []byte) error {
	if len(data) != 0 && data[0] == '[' {
		var refs []struct{}
		if err := json.Unmarshal(data, &refs); err != nil {
			return err
		}
		count := len(refs)
		a.references = &count
		return nil
	}
	return json.Unmarshal(data, &a.number)
}

func (a activeCount) valid(kind string) bool {
	switch kind {
	case "Job":
		return a.references == nil
	case "CronJob":
		return a.number == nil
	default:
		return a.number == nil && a.references == nil
	}
}
