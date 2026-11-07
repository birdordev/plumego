package main

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var (
	docPaths = []string{
		"README.md",
		"README_CN.md",
		"docs/start/getting-started.md",
		"docs/reference/canonical-style-guide.md",
		"docs/modules/core/README.md",
	}

	packageMainPattern = regexp.MustCompile(`(?m)^package\s+main(\s|$)`)
	sanitizePattern    = regexp.MustCompile(`[^A-Za-z0-9]+`)
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	root, err := os.Getwd()
	if err != nil {
		return err
	}

	tmp, err := os.MkdirTemp("", "plumego-doc-snippets.*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	if err := writeModuleFile(tmp, root); err != nil {
		return err
	}

	count, err := writePackageMainSnippets(tmp, root)
	if err != nil {
		return err
	}

	fragmentCount, err := writeGettingStartedFragments(tmp, root)
	if err != nil {
		return err
	}
	count += fragmentCount

	if count == 0 {
		return fmt.Errorf("no package-main Go documentation snippets found")
	}

	cmd := exec.Command("go", "test", "./...")
	cmd.Dir = tmp
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}

	fmt.Printf("compiled %d Go documentation snippets\n", count)
	return nil
}

func writeModuleFile(tmp string, root string) error {
	content := fmt.Sprintf(`module github.com/spcent/plumego-doc-snippets

go 1.26.0

require github.com/spcent/plumego v0.0.0

replace github.com/spcent/plumego => %s
`, strconv.Quote(root))
	return os.WriteFile(filepath.Join(tmp, "go.mod"), []byte(content), 0o644)
}

func writePackageMainSnippets(tmp string, root string) (int, error) {
	total := 0
	for _, doc := range docPaths {
		blocks, err := goBlocks(filepath.Join(root, doc))
		if err != nil {
			if os.IsNotExist(err) {
				return 0, fmt.Errorf("missing doc: %s", doc)
			}
			return 0, err
		}

		snippetIndex := 0
		id := sanitize(doc)
		for _, block := range blocks {
			if !isPackageMain(block) {
				continue
			}
			snippetIndex++
			total++

			dir := filepath.Join(tmp, fmt.Sprintf("%s-%d", id, snippetIndex))
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return 0, err
			}
			if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(block), 0o644); err != nil {
				return 0, err
			}
		}
	}
	return total, nil
}

func writeGettingStartedFragments(tmp string, root string) (int, error) {
	blocks, err := goBlocks(filepath.Join(root, "docs/start/getting-started.md"))
	if err != nil {
		if os.IsNotExist(err) {
			return 0, fmt.Errorf("missing doc: docs/start/getting-started.md")
		}
		return 0, err
	}

	count := 0
	for _, block := range blocks {
		if strings.TrimSpace(block) == "" || isPackageMain(block) {
			continue
		}

		count++
		dir := filepath.Join(tmp, fmt.Sprintf("docs-getting-started-fragment-%d", count))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return 0, err
		}
		if err := os.WriteFile(filepath.Join(dir, "main.go"), wrappedFragment(block), 0o644); err != nil {
			return 0, err
		}
	}
	return count, nil
}

func goBlocks(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	var blocks []string
	var snippet bytes.Buffer
	inGo := false

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		marker := strings.TrimSpace(line)
		switch {
		case !inGo && marker == "```go":
			inGo = true
			snippet.Reset()
		case inGo && marker == "```":
			inGo = false
			blocks = append(blocks, snippet.String())
		case inGo:
			snippet.WriteString(line)
			snippet.WriteByte('\n')
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return blocks, nil
}

func wrappedFragment(block string) []byte {
	var body strings.Builder
	for _, line := range strings.SplitAfter(block, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == `import "github.com/spcent/plumego/router"` {
			continue
		}
		body.WriteString(line)
	}

	var out bytes.Buffer
	out.WriteString(`package main

import (
	"log"
	"net/http"

	"github.com/spcent/plumego"
	"github.com/spcent/plumego/core"
	"github.com/spcent/plumego/router"
)

func main() {
	app := core.New(core.DefaultConfig(), core.AppDependencies{})
	_ = router.Param
	_ = plumego.Param
`)
	out.WriteString(body.String())
	out.WriteString("}\n")
	return out.Bytes()
}

func isPackageMain(block string) bool {
	return packageMainPattern.MatchString(block)
}

func sanitize(value string) string {
	value = sanitizePattern.ReplaceAllString(value, "-")
	value = strings.Trim(value, "-")
	if value == "" {
		return "snippet"
	}
	return value
}
