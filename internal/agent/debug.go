package agent

import (
	"encoding/json"
	"os"
)

// PrintJSON 把任意值以缩进 JSON 打到 stdout，给 --check 调试用。
func PrintJSON(v interface{}) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}
