package ctlcmd

import (
	"errors"
	"io"
	"strings"
)

// readLine 逐字节读到换行为止，不多读 —— stdin 上后面的内容留给别人。
func readLine(r io.Reader) (string, error) {
	var b strings.Builder
	buf := make([]byte, 1)
	for {
		n, err := r.Read(buf)
		if n == 1 {
			if buf[0] == '\n' {
				return strings.TrimSuffix(b.String(), "\r"), nil
			}
			b.WriteByte(buf[0])
		}
		if err != nil {
			if errors.Is(err, io.EOF) && b.Len() > 0 {
				return b.String(), nil
			}
			return b.String(), err
		}
	}
}
