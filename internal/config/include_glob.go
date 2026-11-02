package config

import (
	"errors"
	"io/fs"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/0xJacky/Nginx-UI/internal/nginx"
	"github.com/uozi-tech/cosy"
)

// maxIncludeGlobPaths bounds the paths one include pattern may expand to while
// it is resolved, so a pattern that revisits directories through ".." cannot
// grow without limit. It is well above maxConfigIncludeFiles, which already
// caps what validation accepts.
const maxIncludeGlobPaths = 4096

var errIncludeGlobTooBroad = errors.New("include pattern expands to too many paths")

// includeFullPath returns the path nginx builds for an include argument:
// ngx_conf_full_name prepends the config prefix to a relative name as text,
// without cleaning it. Cleaning would fold "dir/../x" lexically, while the
// kernel follows ".." from wherever "dir" really points.
func includeFullPath(base, pattern string) string {
	if filepath.IsAbs(pattern) {
		return pattern
	}
	if runtime.GOOS == "windows" {
		return filepath.Join(base, pattern)
	}
	return strings.TrimSuffix(base, "/") + "/" + pattern
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
	// nginx for Windows does not use glob(3); keep Go's matcher there.
	if runtime.GOOS == "windows" {
		return nginx.Glob(translated)
	}
	return globLikeGlibc(translated)
}

// globLikeGlibc expands a pattern (already in Go's bracket syntax) the way
// glibc glob(3) does with the flags nginx passes, reading directories through
// the nginx target filesystem so local and SFTP targets behave the same.
// filepath.Glob and sftp.Glob differ from glibc in ways that would let nginx
// load files the validator never sees:
//   - glibc's readdir returns "." and "..", so a wildcard component such as
//     ".*" matches them; Go drops those entries.
//   - nginx never calls setlocale, so glibc matches byte by byte; Go's "?" and
//     "[...]" consume a whole UTF-8 rune.
//
// Components without wildcards are joined as written, "." and ".." included,
// and the path is never cleaned, so ".." is followed by the kernel exactly as
// nginx's open() follows it. Like glibc without GLOB_PERIOD, a wildcard does
// not match a leading "." unless the component starts with a literal ".".
func globLikeGlibc(pattern string) ([]string, error) {
	components := strings.Split(strings.TrimPrefix(pattern, "/"), "/")
	for _, component := range components {
		if hasGlobMeta(component) {
			// Reject malformed components even when no directory is read.
			if _, err := path.Match(byteRunes(component), ""); err != nil {
				return nil, err
			}
		}
	}

	paths := []string{""}
	if strings.HasPrefix(pattern, "/") {
		paths[0] = "/"
	}
	lastLiteral := false
	for _, component := range components {
		lastLiteral = !hasGlobMeta(component)
		var next []string
		if lastLiteral {
			name := unescapeGlob(component)
			for _, dir := range paths {
				next = append(next, joinGlobPath(dir, name))
			}
		} else {
			matcher := byteRunes(component)
			explicitDot := strings.HasPrefix(component, ".") || strings.HasPrefix(component, `\.`)
			for _, dir := range paths {
				readPath := dir
				if readPath == "" {
					readPath = "."
				}
				entries, err := nginx.ReadDir(readPath)
				if err != nil {
					continue
				}
				names := make([]string, 0, len(entries)+2)
				names = append(names, ".", "..")
				for _, entry := range entries {
					names = append(names, entry.Name())
				}
				for _, name := range names {
					if strings.HasPrefix(name, ".") && !explicitDot {
						continue
					}
					matched, err := path.Match(matcher, byteRunes(name))
					if err != nil {
						return nil, err
					}
					if matched {
						next = append(next, joinGlobPath(dir, name))
					}
				}
			}
		}
		if len(next) > maxIncludeGlobPaths {
			return nil, errIncludeGlobTooBroad
		}
		paths = next
	}

	// Names produced by a wildcard came from a directory listing; a trailing
	// literal name still has to exist, as glob(3) checks it.
	matches := paths
	if lastLiteral {
		matches = matches[:0]
		for _, candidate := range paths {
			if _, err := nginx.Lstat(candidate); err == nil {
				matches = append(matches, candidate)
			}
		}
	}
	sort.Strings(matches)
	return matches, nil
}

func joinGlobPath(dir, name string) string {
	if dir == "" {
		return name
	}
	if strings.HasSuffix(dir, "/") {
		return dir + name
	}
	return dir + "/" + name
}

// hasGlobMeta reports an unescaped "*", "?" or "[" in a path component. An
// unterminated "[" counts as well, so Go's matcher rejects it and validation
// fails closed.
func hasGlobMeta(component string) bool {
	for i := 0; i < len(component); i++ {
		switch component[i] {
		case '\\':
			i++
		case '*', '?', '[':
			return true
		}
	}
	return false
}

// unescapeGlob drops the backslashes glob(3) treats as escapes in a component
// without wildcards.
func unescapeGlob(component string) string {
	if !strings.Contains(component, `\`) {
		return component
	}
	var out strings.Builder
	for i := 0; i < len(component); i++ {
		if component[i] == '\\' && i+1 < len(component) {
			i++
		}
		out.WriteByte(component[i])
	}
	return out.String()
}

// byteRunes maps every byte to the rune of the same value, so path.Match
// compares byte by byte, as glibc does in the C locale nginx runs in.
func byteRunes(s string) string {
	var out strings.Builder
	out.Grow(len(s) * 2)
	for i := 0; i < len(s); i++ {
		out.WriteRune(rune(s[i]))
	}
	return out.String()
}

// glibcGlobPattern converts an include pattern from the glob(3) syntax nginx
// resolves includes with into the syntax of Go's matcher. Where the two read a
// pattern differently, Go matches nothing while nginx loads files that would
// then escape validation, so every bracket expression is checked:
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
