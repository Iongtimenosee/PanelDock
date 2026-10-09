package main

// 文档守门：行数上限、中英 README 结构一致、AGENTS.md 的文档引用有效、注释占比。
// 这些约束的理由与「想放宽该怎么办」见 docs/doc-policy.md。
//
// 本文件不依赖 Windows API，任何平台都能跑：go test -run TestDocs .

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// docLineLimits 是各文档的硬上限（行）。
// 超出时先压缩现有内容，不要改这个数字。
var docLineLimits = map[string]int{
	"AGENTS.md":            150,
	"docs/architecture.md": 200,
	"docs/behavior.md":     150,
	"docs/pitfalls.md":     250,
	"docs/doc-policy.md":   120,
	"README.md":            140,
	"README.zh-CN.md":      140,
}

// maxCommentRatio 是注释行占比上限。只约束规模够大的文件 ——
// 几十行的小文件（aumid_windows.go 之类）注释天然占比高，按比例卡只会逼人删掉有用的说明。
const maxCommentRatio = 0.25

// minLinesForCommentRatio 是注释占比检查的生效下限（行）。
const minLinesForCommentRatio = 200

func TestDocsLineLimits(t *testing.T) {
	for path, limit := range docLineLimits {
		lines := countLines(t, path)
		if lines > limit {
			t.Errorf("%s 有 %d 行，超过上限 %d 行（上限见 docs/doc-policy.md；超了请压缩内容，不要改上限）", path, lines, limit)
		}
	}
}

// TestDocsNoPlaceholder 挡住「以后再写」的占位文本 —— 文档里留空节比没有这一节更糟。
func TestDocsNoPlaceholder(t *testing.T) {
	bad := []string{"待补充", "TODO", "FIXME", "TBD", "待定"}
	for path := range docLineLimits {
		if path == "docs/doc-policy.md" {
			// 规范文件本身必然列举被禁的词，跳过它自己的规则。
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("读 %s: %v", path, err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			for _, marker := range bad {
				if strings.Contains(line, marker) {
					t.Errorf("%s:%d 含占位文本 %q", path, i+1, marker)
				}
			}
		}
	}
}

// TestReadmeHeadingParity 保证两份 README 的章节结构逐层一致。
// 翻译可以措辞不同，但不能少一节、多一节或层级错位。
func TestReadmeHeadingParity(t *testing.T) {
	en := headingLevels(t, "README.md")
	zh := headingLevels(t, "README.zh-CN.md")
	if len(zh) == 0 || len(en) == 0 {
		t.Fatal("两份 README 至少有一份没解析出标题")
	}
	if len(zh) != len(en) {
		t.Fatalf("README 章节数不同：中文 %d 节、英文 %d 节", len(zh), len(en))
	}
	for i := range zh {
		if zh[i] != en[i] {
			t.Errorf("第 %d 节层级不同：中文 h%d、英文 h%d", i+1, zh[i], en[i])
		}
	}
}

// TestAgentsDocLinksExist 保证 AGENTS.md 指向的文档真实存在（改名/删文件不会留下死链）。
func TestAgentsDocLinksExist(t *testing.T) {
	data, err := os.ReadFile("AGENTS.md")
	if err != nil {
		t.Fatalf("读 AGENTS.md: %v", err)
	}
	re := regexp.MustCompile(`docs/[a-z0-9-]+\.md`)
	seen := map[string]bool{}
	for _, m := range re.FindAllString(string(data), -1) {
		seen[m] = true
	}
	if len(seen) == 0 {
		t.Fatal("AGENTS.md 里没找到任何 docs/*.md 引用：文档索引断了")
	}
	for path := range seen {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("AGENTS.md 引用了不存在的 %s", path)
		}
	}
}

// TestCommentRatio 挡住「注释比代码多」的膨胀。注释 > 代码通常意味着代码该拆，不是注释该加。
func TestCommentRatio(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") || path == "docs_test.go" {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(string(data), "\n")
		comment := 0
		for _, line := range lines {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				comment++
			}
		}
		if len(lines) < minLinesForCommentRatio {
			continue
		}
		if ratio := float64(comment) / float64(len(lines)); ratio > maxCommentRatio {
			t.Errorf("%s 注释占比 %.0f%%（%d/%d 行），超过 %.0f%%：删掉复述文档的注释，改成指针",
				path, ratio*100, comment, len(lines), maxCommentRatio*100)
		}
	}
}

func countLines(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读 %s: %v", path, err)
	}
	return len(strings.Split(string(data), "\n"))
}

// headingLevels 返回文档里所有 Markdown 标题的层级序列，跳过代码块内的 # 行。
func headingLevels(t *testing.T, path string) []int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读 %s: %v", path, err)
	}
	var out []int
	inFence := false
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "```") {
			inFence = !inFence
			continue
		}
		if inFence || !strings.HasPrefix(line, "#") {
			continue
		}
		level := 0
		for level < len(line) && line[level] == '#' {
			level++
		}
		if level > 6 {
			continue
		}
		out = append(out, level)
	}
	return out
}
