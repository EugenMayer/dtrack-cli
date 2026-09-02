package commands

import (
	"encoding/json"
	"io"
)

// printJSON writes v to out as indented JSON, terminated by a newline.
func printJSON(out io.Writer, v any) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
