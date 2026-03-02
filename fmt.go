package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

const (
	dim   = "\033[2m"
	reset = "\033[0m"
)

func runFmt(args []string) int {
	initTelemetry()
	ev := Event{Cmd: "fmt", OK: true}
	defer func() { emit(ev) }()

	FormatStream(os.Stdin, os.Stdout)
	return 0
}

// FormatStream reads stream-json events from r and writes formatted output to w.
func FormatStream(r io.Reader, w io.Writer) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)

	lastWasTool := false

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var ev map[string]json.RawMessage
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}

		evType := jsonString(ev, "type")

		if evType == "assistant" {
			msgRaw, ok := ev["message"]
			if !ok {
				continue
			}

			var msg struct {
				Content []struct {
					Type  string          `json:"type"`
					Text  string          `json:"text"`
					Name  string          `json:"name"`
					Input json.RawMessage `json:"input"`
				} `json:"content"`
			}
			if err := json.Unmarshal(msgRaw, &msg); err != nil {
				continue
			}

			for _, block := range msg.Content {
				switch block.Type {
				case "text":
					if block.Text == "" {
						continue
					}
					if lastWasTool {
						fmt.Fprint(w, "\n")
					}
					fmt.Fprint(w, block.Text)
					lastWasTool = false

				case "tool_use":
					detail := extractToolDetail(block.Name, block.Input)
					prefix := "\n"
					if lastWasTool {
						prefix = ""
					}
					out := fmt.Sprintf("%s%s▶ %s", prefix, dim, block.Name)
					if detail != "" {
						out += "  " + detail
					}
					out += reset
					fmt.Fprintln(w, out)
					lastWasTool = true
				}
			}
		}
	}
}

func extractToolDetail(name string, input json.RawMessage) string {
	if input == nil {
		return ""
	}
	var inp map[string]json.RawMessage
	if err := json.Unmarshal(input, &inp); err != nil {
		return ""
	}

	switch name {
	case "Bash":
		cmd := jsonString(inp, "command")
		if len(cmd) > 80 {
			cmd = cmd[:80]
		}
		return cmd
	case "Read", "Edit", "Write":
		return jsonString(inp, "file_path")
	case "Grep":
		return jsonString(inp, "pattern")
	case "Glob":
		return jsonString(inp, "pattern")
	case "Agent":
		return jsonString(inp, "description")
	default:
		return ""
	}
}
