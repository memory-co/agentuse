package release

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
)

const GitHubDownloadURL = "https://github.com/memory-co/agentuse/releases/download"
const MaxChecksumBytes = 1 << 20

func Checksums(r io.Reader) (map[string]string, error) {
	sums := map[string]string{}
	reader := &io.LimitedReader{R: r, N: MaxChecksumBytes + 1}
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 {
			return nil, errors.New("SHA256SUMS 格式无效")
		}
		name := strings.TrimPrefix(fields[1], "*")
		if _, exists := sums[name]; exists {
			return nil, errors.New("SHA256SUMS 包含重复文件")
		}
		digest, err := hex.DecodeString(fields[0])
		if err != nil || len(digest) != sha256.Size {
			return nil, errors.New("SHA256SUMS 摘要无效")
		}
		sums[name] = strings.ToLower(fields[0])
	}
	if reader.N == 0 {
		return nil, errors.New("SHA256SUMS 文件过大")
	}
	return sums, scanner.Err()
}
