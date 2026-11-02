package config

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/0xJacky/Nginx-UI/internal/nginx"
	"github.com/0xJacky/Nginx-UI/settings"
	"github.com/uozi-tech/cosy"
)

const (
	maxConfigIncludeDepth = 16
	maxConfigIncludeFiles = 1024
	maxConfigIncludeBytes = 64 << 20
)

type configIncludeValidator struct {
	base    string
	active  map[string]bool
	visited map[string]bool
	bytes   int
	files   int
}

// validateConfigIncludes applies the directive policy to existing include
// targets as well as the edited file. Relative includes use Nginx's main config
// prefix, not the directory of the file containing the include. Globs with no
// matches remain valid, and safe system snippets outside the conf root work.
func validateConfigIncludes(path string, content []byte) error {
	validator := configIncludeValidator{base: includeBasePath(), active: make(map[string]bool), visited: make(map[string]bool)}
	return validator.validate(path, content, 0)
}

// includeBasePath returns the directory relative include patterns resolve
// against: Nginx's main config prefix.
func includeBasePath() string {
	if settings.NginxSettings.ConfigPath != "" && filepath.IsAbs(settings.NginxSettings.ConfigPath) {
		return filepath.Dir(settings.NginxSettings.ConfigPath)
	}
	return nginx.GetConfPath()
}

// canonicalIncludePath returns the path with symlinks resolved when possible,
// so one file reached through different paths is recognized as the same.
func canonicalIncludePath(path string) string {
	if resolved, err := nginx.EvalSymlinks(path); err == nil {
		return filepath.Clean(resolved)
	}
	return filepath.Clean(path)
}

func (validator *configIncludeValidator) validate(path string, content []byte, depth int) error {
	if depth > maxConfigIncludeDepth || validator.files >= maxConfigIncludeFiles || validator.bytes+len(content) > maxConfigIncludeBytes {
		return fmt.Errorf("nginx include validation limit exceeded")
	}
	canonical := canonicalIncludePath(path)
	if validator.active[canonical] {
		return fmt.Errorf("cyclic nginx include: %s", path)
	}
	if validator.visited[canonical] {
		return nil
	}
	validator.active[canonical] = true
	defer delete(validator.active, canonical)
	validator.files++
	validator.bytes += len(content)
	if err := ValidateConfigContentBytes(content); err != nil {
		return err
	}
	if err := visitConfigStatementsAsNginx(content, func(statement []string) error {
		if len(statement) != 2 || !strings.EqualFold(statement[0], "include") {
			return nil
		}
		pattern := statement[1]
		if !filepath.IsAbs(pattern) {
			pattern = filepath.Join(validator.base, pattern)
		}
		matches, err := resolveIncludePattern(pattern)
		if err != nil {
			if _, ok := errors.AsType[*cosy.Error](err); ok {
				return err
			}
			return fmt.Errorf("resolve nginx include %s: %w", pattern, err)
		}
		for _, match := range matches {
			info, err := nginx.Stat(match)
			if err != nil {
				return fmt.Errorf("stat nginx include %s: %w", match, err)
			}
			if info.Size() > int64(maxConfigIncludeBytes-validator.bytes) {
				return fmt.Errorf("nginx include validation limit exceeded")
			}
			included, err := nginx.ReadFile(match)
			if err != nil {
				return fmt.Errorf("read nginx include %s: %w", match, err)
			}
			if err = validator.validate(match, included, depth+1); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	validator.visited[canonical] = true
	return nil
}

type includeWalker struct {
	base   string
	active map[string]bool
	bytes  int
	files  int
	visit  func(statement []string, depth int)
}

// VisitStatementsWithIncludes calls visit for every statement of a
// configuration and, right after each include, for the statements of the files
// it matches, so the statements arrive in the order Nginx reads them. depth is
// 0 for the statements of content itself and grows by one per include level.
// path may be empty when content is not saved yet.
//
// The walk never fails: includes built from variables, globs without matches,
// and files that cannot be read are skipped, and expansion stops at include
// cycles and at the limits the include validation applies.
func VisitStatementsWithIncludes(path string, content []byte, visit func(statement []string, depth int)) {
	walker := includeWalker{base: includeBasePath(), active: make(map[string]bool), visit: visit}
	walker.walk(path, content, 0)
}

func (walker *includeWalker) walk(path string, content []byte, depth int) {
	if path != "" {
		canonical := canonicalIncludePath(path)
		if walker.active[canonical] {
			return
		}
		walker.active[canonical] = true
		defer delete(walker.active, canonical)
	}
	walker.files++
	walker.bytes += len(content)

	_ = visitConfigStatementsAsNginx(content, func(statement []string) error {
		if len(statement) == 0 {
			return nil
		}
		walker.visit(statement, depth)
		if len(statement) == 2 && strings.EqualFold(statement[0], "include") && depth < maxConfigIncludeDepth {
			walker.include(statement[1], depth+1)
		}
		return nil
	})
}

func (walker *includeWalker) include(pattern string, depth int) {
	if strings.Contains(pattern, "$") {
		return
	}
	if !filepath.IsAbs(pattern) {
		pattern = filepath.Join(walker.base, pattern)
	}
	matches, err := resolveIncludePattern(pattern)
	if err != nil {
		return
	}
	for _, match := range matches {
		if walker.files >= maxConfigIncludeFiles {
			return
		}
		info, err := nginx.Stat(match)
		if err != nil || info.IsDir() || info.Size() > int64(maxConfigIncludeBytes-walker.bytes) {
			continue
		}
		included, err := nginx.ReadFile(match)
		if err != nil || walker.bytes+len(included) > maxConfigIncludeBytes {
			continue
		}
		walker.walk(match, included, depth)
	}
}

// resolveIncludePattern returns the files nginx would read for an include
// argument. nginx opens the argument literally unless it contains one of
// "*?[", and only then resolves it with glob(3). Go's Glob would read a
// backslash in a literal path as an escape and look up a different name, so
// literal paths are checked as they are.
func resolveIncludePattern(pattern string) ([]string, error) {
	if !strings.ContainsAny(pattern, "*?[") {
		if _, err := nginx.Lstat(pattern); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil, nil
			}
			return nil, err
		}
		return []string{pattern}, nil
	}
	translated, err := glibcGlobPattern(pattern)
	if err != nil {
		return nil, err
	}
	return nginx.Glob(translated)
}

// glibcGlobPattern converts an include pattern from the glob(3) syntax nginx
// resolves includes with into the syntax of Go's matcher, which the local and
// the SFTP Glob both use. Where the two read a pattern differently, Go matches
// nothing while nginx loads files that would then escape validation, so every
// bracket expression is checked:
//   - the negated form "[!...]" is rewritten to "[^...]";
//   - POSIX character classes "[:name:]", collating symbols "[.x.]" and
//     equivalence classes "[=x=]" have no Go equivalent and are rejected.
//
// Other malformed brackets are copied unchanged: Go rejects them with
// ErrBadPattern, so validation still fails closed.
func glibcGlobPattern(pattern string) (string, error) {
	var out strings.Builder
	out.Grow(len(pattern))
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		if c == '\\' && i+1 < len(pattern) {
			out.WriteByte(c)
			i++
			out.WriteByte(pattern[i])
			continue
		}
		out.WriteByte(c)
		if c != '[' {
			continue
		}
		i++
		if i < len(pattern) && pattern[i] == '!' {
			out.WriteByte('^')
			i++
		}
		for ; i < len(pattern); i++ {
			c = pattern[i]
			if c == '\\' && i+1 < len(pattern) {
				out.WriteByte(c)
				i++
				out.WriteByte(pattern[i])
				continue
			}
			if c == '[' && i+1 < len(pattern) && strings.IndexByte(":.=", pattern[i+1]) >= 0 {
				return "", cosy.WrapErrorWithParams(ErrConfigIncludeGlobUnsupported, pattern)
			}
			out.WriteByte(c)
			if c == ']' {
				break
			}
		}
	}
	return out.String(), nil
}
