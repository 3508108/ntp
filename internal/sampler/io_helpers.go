package sampler

import (
	"io"
	"os"
)

// readBodyLimited читає до n байтів із тіла HTTP-відповіді (захисний ліміт від NIST-ендпоїнта).
func readBodyLimited(r io.Reader, n int64) string {
	if r == nil {
		return ""
	}
	b, err := io.ReadAll(io.LimitReader(r, n))
	if err != nil {
		return ""
	}
	return string(b)
}

// statFile повертає os.FileInfo для шляху; у разі помилки — nil і error (як os.Stat).
func statFile(path string) (os.FileInfo, error) {
	return os.Stat(path)
}
