// Ccview serves a page that shows a C or C++ file next to the Go that
// cmd/cc writes for it. Each translation first rebuilds cmd/cc from the
// working tree, so reloading the page shows what the generator does now.
//
// Usage:
//
//	misc/ccview/ccview [-addr localhost:8787] [-root ~/git/go-cfront] [dir or file]...
//
// The files offered are cmd/cc's testdata (thin, cxx, the top-level
// C programs and slimcc's tests) and any directories or files named on
// the command line.
package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

//go:embed index.html
var indexHTML []byte

var (
	addr = flag.String("addr", "localhost:8787", "listen on `addr`")
	root = flag.String("root", filepath.Join(os.Getenv("HOME"), "git/go-cfront"), "the Go tree whose cmd/cc to build")
)

var (
	work  string // ~/.cache/ccview
	ccBin string

	mu       sync.Mutex // serializes builds and translations
	lastGo   = map[string]string{}
	prevGo   = map[string]string{}
	extra    []string
	buildEnv []string
)

func main() {
	flag.Parse()
	extra = flag.Args()
	home, _ := os.UserHomeDir()
	work = filepath.Join(home, ".cache", "ccview")
	ccBin = filepath.Join(work, "cc")
	tmp := filepath.Join(work, "tmp")
	if err := os.MkdirAll(tmp, 0o777); err != nil {
		log.Fatal(err)
	}
	// The fork's go, its own cache, and a TMPDIR off /tmp (which fills up).
	buildEnv = append(os.Environ(),
		"PATH="+filepath.Join(*root, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"),
		"GOROOT="+*root,
		"GOCACHE="+filepath.Join(home, ".cache", "cfront-gocache"),
		"TMPDIR="+tmp,
		"GOFLAGS=-buildvcs=false",
	)

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(indexHTML)
	})
	http.HandleFunc("/api/files", handleFiles)
	http.HandleFunc("/api/translate", handleTranslate)
	log.Printf("ccview: http://%s/ (cmd/cc from %s)", *addr, *root)
	log.Fatal(http.ListenAndServe(*addr, nil))
}

type fileGroup struct {
	Name  string   `json:"name"`
	Files []string `json:"files"`
}

func isSource(p string) bool {
	switch filepath.Ext(p) {
	case ".c", ".cc", ".cpp", ".cxx":
		return true
	}
	return false
}

func listDir(dir string) []string {
	ents, _ := os.ReadDir(dir)
	var out []string
	for _, e := range ents {
		if !e.IsDir() && isSource(e.Name()) {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(out)
	return out
}

func handleFiles(w http.ResponseWriter, r *http.Request) {
	td := filepath.Join(*root, "src/cmd/cc/testdata")
	groups := []fileGroup{
		{"testdata/thin", listDir(filepath.Join(td, "thin"))},
		{"testdata/cxx", listDir(filepath.Join(td, "cxx"))},
		{"testdata", listDir(td)},
		{"testdata/slimcc", listDir(filepath.Join(td, "slimcc"))},
	}
	for _, x := range extra {
		x, _ = filepath.Abs(x)
		if st, err := os.Stat(x); err == nil && st.IsDir() {
			groups = append(groups, fileGroup{x, listDir(x)})
		} else if err == nil {
			groups = append(groups, fileGroup{filepath.Dir(x), []string{x}})
		}
	}
	writeJSON(w, groups)
}

type decl struct {
	Kind  string `json:"kind"` // func, type, var, const
	Go    string `json:"go"`   // Go name
	C     string `json:"c"`    // C or demangled C++ name, best guess
	Start int    `json:"start"`
	End   int    `json:"end"` // 1-based, inclusive
}

type result struct {
	Path     string `json:"path"`
	Lang     string `json:"lang"` // c or cpp
	Source   string `json:"source"`
	Go       string `json:"go"`
	Prev     string `json:"prev"` // the Go of the last translation that differed
	Stderr   string `json:"stderr"`
	Build    string `json:"build"` // output of a failed build of cmd/cc
	Rev      string `json:"rev"`
	BuildMS  int64  `json:"buildMs"`
	TransMS  int64  `json:"transMs"`
	Decls    []decl `json:"decls"`
	ParseErr string `json:"parseErr"`
	Stats    []stat `json:"stats"`
}

type stat struct {
	Name string `json:"name"`
	N    int    `json:"n"`
}

type request struct {
	Path   string `json:"path"`
	Source string `json:"source"` // scratch source, used when non-empty
	Lang   string `json:"lang"`   // of scratch source
}

func handleTranslate(w http.ResponseWriter, r *http.Request) {
	var req request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	mu.Lock()
	defer mu.Unlock()

	var res result
	res.Rev = revision()
	t0 := time.Now()
	if out, err := buildCC(); err != nil {
		res.Build = out
		if _, serr := os.Stat(ccBin); serr != nil {
			writeJSON(w, res)
			return
		}
		res.Build += "\n(using the last cc that built)"
	}
	res.BuildMS = time.Since(t0).Milliseconds()

	path := req.Path
	if req.Source != "" {
		ext := ".c"
		if req.Lang == "cpp" {
			ext = ".cc"
		}
		path = filepath.Join(work, "scratch"+ext)
		if err := os.WriteFile(path, []byte(req.Source), 0o666); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	}
	src, err := os.ReadFile(path)
	if err != nil {
		http.Error(w, err.Error(), 404)
		return
	}
	res.Path, res.Source = path, string(src)
	res.Lang = "c"
	if filepath.Ext(path) != ".c" {
		res.Lang = "cpp"
	}

	out := filepath.Join(work, "out.go")
	os.Remove(out)
	cmd := exec.Command(ccBin, "-main", "-undef", "-o", out, path)
	cmd.Env = append(buildEnv, "CCTHIN=1") // the 1:1 translation, whether or not it is cc's default yet
	cmd.Dir = filepath.Dir(path)
	var stderr bytes.Buffer
	cmd.Stderr, cmd.Stdout = &stderr, &stderr
	t1 := time.Now()
	runErr := cmd.Run()
	res.TransMS = time.Since(t1).Milliseconds()
	res.Stderr = stderr.String()
	if runErr != nil && res.Stderr == "" {
		res.Stderr = runErr.Error()
	}
	gosrc, _ := os.ReadFile(out)
	res.Go = string(gosrc)

	key := path
	if res.Go != "" && res.Go != lastGo[key] {
		if lastGo[key] != "" {
			prevGo[key] = lastGo[key]
		}
		lastGo[key] = res.Go
	}
	res.Prev = prevGo[key]

	res.Decls, res.ParseErr = decls(gosrc)
	res.Stats = stats(res.Go)
	writeJSON(w, res)
}

// buildCC rebuilds cmd/cc into ccBin. The go command's cache makes this
// quick when nothing changed.
func buildCC() (string, error) {
	cmd := exec.Command(filepath.Join(*root, "bin", "go"), "build", "-o", ccBin, "./cmd/cc")
	cmd.Dir = filepath.Join(*root, "src")
	cmd.Env = buildEnv
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func revision() string {
	cmd := exec.Command("git", "log", "-1", "--format=%h %s")
	cmd.Dir = *root
	out, _ := cmd.Output()
	rev := strings.TrimSpace(string(out))
	cmd = exec.Command("git", "status", "--porcelain", "--", "src/cmd/cc")
	cmd.Dir = *root
	if st, _ := cmd.Output(); len(bytes.TrimSpace(st)) > 0 {
		rev += " (cmd/cc modified)"
	}
	return rev
}

var namePrefix = regexp.MustCompile(`^(fd_)?[cg]\d*_`)

// decls lists the top-level declarations of the generated Go, with a
// guess at the C name each comes from.
func decls(src []byte) ([]decl, string) {
	if len(src) == 0 {
		return nil, ""
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "out.go", src, parser.ParseComments|parser.SkipObjectResolution)
	if f == nil {
		return nil, fmt.Sprint(err)
	}
	var ds []decl
	add := func(kind, name string, from, to token.Pos) {
		if name == "_" {
			return
		}
		ds = append(ds, decl{Kind: kind, Go: name, Start: fset.Position(from).Line, End: fset.Position(to).Line})
	}
	for _, d := range f.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			from := d.Pos()
			if d.Doc != nil {
				from = d.Doc.Pos()
			}
			add("func", d.Name.Name, from, d.End())
		case *ast.GenDecl:
			kind := d.Tok.String()
			if kind == "import" {
				continue
			}
			for _, s := range d.Specs {
				from, to := s.Pos(), s.End()
				if len(d.Specs) == 1 {
					from, to = d.Pos(), d.End()
				}
				switch s := s.(type) {
				case *ast.TypeSpec:
					add(kind, s.Name.Name, from, to)
				case *ast.ValueSpec:
					for _, n := range s.Names {
						add(kind, n.Name, from, to)
					}
				}
			}
		}
	}
	// Guess C names: strip cc's prefixes, demangle C++ names in one c++filt.
	var mangled []int
	for i := range ds {
		n := ds[i].Go
		switch {
		case strings.HasPrefix(n, "struct_"):
			n = strings.TrimPrefix(n, "struct_")
		case strings.HasPrefix(n, "union_"):
			n = strings.TrimPrefix(n, "union_")
		default:
			n = namePrefix.ReplaceAllString(n, "")
		}
		ds[i].C = n
		if strings.HasPrefix(n, "_Z") {
			mangled = append(mangled, i)
		}
	}
	if len(mangled) > 0 {
		var in strings.Builder
		for _, i := range mangled {
			in.WriteString(ds[i].C + "\n")
		}
		cmd := exec.Command("c++filt")
		cmd.Stdin = strings.NewReader(in.String())
		if out, err := cmd.Output(); err == nil {
			lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
			if len(lines) == len(mangled) {
				for k, i := range mangled {
					ds[i].C = lines[k]
				}
			}
		}
	}
	errStr := ""
	if err != nil {
		errStr = err.Error()
	}
	return ds, errStr
}

var statPats = []struct {
	name string
	re   *regexp.Regexp
}{
	{"unsafe.Add", regexp.MustCompile(`unsafe\.Add\(`)},
	{"unsafe.Pointer", regexp.MustCompile(`unsafe\.Pointer\b`)},
	{"uintptr", regexp.MustCompile(`\buintptr\(`)},
	{"c23.*", regexp.MustCompile(`\bc23\.\w+`)},
}

func stats(src string) []stat {
	s := []stat{{"lines", strings.Count(src, "\n")}}
	for _, p := range statPats {
		s = append(s, stat{p.name, len(p.re.FindAllStringIndex(src, -1))})
	}
	return s
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(v)
}
