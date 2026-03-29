package claudecode

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"sync"
)

//go:embed cli_tools.json
var embeddedCLITools []byte

var (
	cliToolsLoadOnce sync.Once
	cliToolsRaw      json.RawMessage
	cliToolsErr      error
)

func GetEmbeddedCLIToolsRaw() (json.RawMessage, bool, error) {
	cliToolsLoadOnce.Do(func() {
		trimmed := bytes.TrimSpace(embeddedCLITools)
		if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
			return
		}

		var tools []json.RawMessage
		if err := json.Unmarshal(trimmed, &tools); err != nil {
			cliToolsErr = fmt.Errorf("invalid Claude Code CLI tools JSON: %w", err)
			return
		}
		if len(tools) == 0 {
			return
		}

		cliToolsRaw = append(json.RawMessage(nil), trimmed...)
	})

	if cliToolsErr != nil {
		return nil, false, cliToolsErr
	}
	if len(cliToolsRaw) == 0 {
		return nil, false, nil
	}
	return append(json.RawMessage(nil), cliToolsRaw...), true, nil
}
