package helper

import (
	"strings"
	"testing"
)

// 裸 bufio.NewScanner 受默认 64KB 单行上限约束，上游返回超长 SSE 行时会
// token too long 断流。NewStreamScanner 必须能读完整行。
func TestNewStreamScannerAllowsLargeStreamLine(t *testing.T) {
	line := strings.Repeat("a", 1<<20) // 1MB 单行

	scanner := NewStreamScanner(strings.NewReader(line + "\n"))
	if !scanner.Scan() {
		t.Fatalf("扫描 1MB 单行失败: %v", scanner.Err())
	}
	if got := len(scanner.Text()); got != 1<<20 {
		t.Fatalf("读到 %d 字节，期望 %d", got, 1<<20)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scanner.Err() = %v", err)
	}
}
