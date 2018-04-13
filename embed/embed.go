// Package embed implements all file embedding logic for github.com/mjibson/esc.
package embed

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"text/template"
)

// Config contains all information needed to run esc.
type Config struct {
	// OutputFile is the file name to write output, else stdout.
	OutputFile string
	// Package name for the generated file.
	Package string
	// Prefix is stripped from filenames.
	Prefix string
	// Ignore is the regexp for files we should ignore (for example `\.DS_Store`).
	Ignore string
	// Include is the regexp for files to include. If provided, only files that
	// match will be included.
	Include string
	// ModTime is the Unix timestamp to override as modification time for all files.
	ModTime string
	// Private, if true, causes autogenerated functions to be unexported.
	Private bool
	// NoCompression, if true, stores the files without compression.
	NoCompression bool

	// Files is the list of files or directories to embed.
	Files []string
}

var modTime *int64

type headerTemplateParams struct {
	Invocation     string
	PackageName    string
	FunctionPrefix string
}

type _escFile struct {
	data     []byte
	local    string
	fileinfo os.FileInfo
}

// Run executes a Config.
func Run(conf *Config) error {
	var err error
	if conf.ModTime != "" {
		i, err := strconv.ParseInt(conf.ModTime, 10, 64)
		if err != nil {
			return fmt.Errorf("modtime must be an integer: %v", err)
		}
		modTime = &i
	}
	var fnames, dirnames []string
	content := make(map[string]_escFile)
	prefix := filepath.ToSlash(conf.Prefix)
	var ignoreRegexp *regexp.Regexp
	if conf.Ignore != "" {
		ignoreRegexp, err = regexp.Compile(conf.Ignore)
		if err != nil {
			return err
		}
	}
	var includeRegexp *regexp.Regexp
	if conf.Include != "" {
		includeRegexp, err = regexp.Compile(conf.Include)
		if err != nil {
			return err
		}
	}
	for _, base := range conf.Files {
		files := []string{base}
		for len(files) > 0 {
			fname := files[0]
			files = files[1:]
			if ignoreRegexp != nil && ignoreRegexp.MatchString(fname) {
				continue
			}
			f, err := os.Open(fname)
			if err != nil {
				return err
			}
			fi, err := f.Stat()
			if err != nil {
				return err
			}
			if fi.IsDir() {
				fis, err := f.Readdir(0)
				if err != nil {
					return err
				}
				for _, fi := range fis {
					files = append(files, filepath.Join(fname, fi.Name()))
				}
			} else if includeRegexp == nil || includeRegexp.MatchString(fname) {
				b, err := ioutil.ReadAll(f)
				if err != nil {
					return err
				}
				fpath := filepath.ToSlash(fname)
				n := strings.TrimPrefix(fpath, prefix)
				n = path.Join("/", n)
				if _, ok := content[n]; ok {
					return fmt.Errorf("%s, %s: duplicate name after prefix removal", n, fpath)
				}
				content[n] = _escFile{data: b, local: fpath, fileinfo: fi}
				fnames = append(fnames, n)
			}
			f.Close()
		}
	}
	sort.Strings(fnames)
	w := new(bytes.Buffer)
	headerText, err := header(conf.Package, !(conf.Private))
	if nil != err {
		return fmt.Errorf("failed to expand autogenerated code: %s", err)
	}
	if _, err := w.Write(headerText); err != nil {
		return fmt.Errorf("failed to write output: %s", err)
	}
	dirs := map[string]bool{"/": true}
	gzipLevel := gzip.BestCompression
	if conf.NoCompression {
		gzipLevel = gzip.NoCompression
	}
	for _, fname := range fnames {
		f := content[fname]
		for b := path.Dir(fname); b != "/"; b = path.Dir(b) {
			dirs[b] = true
		}
		var buf bytes.Buffer
		gw, err := gzip.NewWriterLevel(&buf, gzipLevel)
		if err != nil {
			return err
		}
		if _, err := gw.Write(f.data); err != nil {
			return err
		}
		if err := gw.Close(); err != nil {
			return err
		}
		t := f.fileinfo.ModTime().Unix()
		if modTime != nil {
			t = *modTime
		}
		fmt.Fprintf(w, `
	%q: {
		local:   %q,
		size:    %v,
		modtime: %v,
		compressed: %s,
	},%s`, fname, f.local, len(f.data), t, segment(&buf), "\n")
	}
	for d := range dirs {
		dirnames = append(dirnames, d)
	}
	sort.Strings(dirnames)
	for _, dir := range dirnames {
		local := path.Join(prefix, dir)
		if len(local) == 0 {
			local = "."
		}
		if local[0] == '/' {
			// Read dirs relative to the go proc's cwd vs system's
			// fs root.
			local = local[1:]
		}
		fmt.Fprintf(w, `
	%q: {
		isDir: true,
		local: %q,
	},%s`, dir, local, "\n")
	}
	w.WriteString(footer)
	out := os.Stdout
	if conf.OutputFile != "" {
		if out, err = os.Create(conf.OutputFile); err != nil {
			return err
		}
	}
	if _, err := w.WriteTo(out); err != nil {
		return err
	}
	if conf.OutputFile != "" {
		return out.Close()
	}
	return nil
}

func segment(s *bytes.Buffer) string {
	var b bytes.Buffer
	b64 := base64.NewEncoder(base64.StdEncoding, &b)
	b64.Write(s.Bytes())
	b64.Close()
	res := "`\n"
	chunk := make([]byte, 80)
	for n, _ := b.Read(chunk); n > 0; n, _ = b.Read(chunk) {
		res += string(chunk[0:n]) + "\n"
	}
	return res + "`"
}

func header(packageName string, enableExports bool) ([]byte, error) {
	functionPrefix := ""
	if !enableExports {
		functionPrefix = "_esc"
	}
	headerParams := headerTemplateParams{
		Invocation:     strings.Join(os.Args[1:], " "),
		PackageName:    packageName,
		FunctionPrefix: functionPrefix,
	}
	tmpl, err := template.New("").Parse(headerTemplate)
	if nil != err {
		return nil, err
	}
	var b bytes.Buffer
	err = tmpl.Execute(&b, headerParams)
	if nil != err {
		return nil, err
	}
	return b.Bytes(), nil
}

const (
	headerTemplate = `// Code generated by "esc {{.Invocation}}"; DO NOT EDIT.

package {{.PackageName}}

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"io/ioutil"
	"net/http"
	"os"
	"path"
	"strings"
	"sync"
	"time"
)

type _escLocalFS struct{}

var _escLocal _escLocalFS

type _escStaticFS struct{}

var _escStatic _escStaticFS

type _escDirectory struct {
	fs   http.FileSystem
	name string
}

type _escFile struct {
	compressed string
	size       int64
	modtime    int64
	local      string
	isDir      bool

	once sync.Once
	data []byte
	name string
}

func (_escLocalFS) Open(name string) (http.File, error) {
	f, present := _escData[path.Clean(name)]
	if !present {
		return nil, os.ErrNotExist
	}
	return os.Open(f.local)
}

func (_escStaticFS) prepare(name string) (*_escFile, error) {
	f, present := _escData[path.Clean(name)]
	if !present {
		return nil, os.ErrNotExist
	}
	err := f.prepare()
	return f, err
}

func (f *_escFile) prepare() error {
	var err error
	f.once.Do(func() {
		f.name = path.Base(f.local)
		if f.size == 0 {
			return
		}
		var gr *gzip.Reader
		b64 := base64.NewDecoder(base64.StdEncoding, bytes.NewBufferString(f.compressed))
		gr, err = gzip.NewReader(b64)
		if err != nil {
			return
		}
		f.data, err = ioutil.ReadAll(gr)
	})
	if err != nil {
		return err
	}
	return nil
}

func (fs _escStaticFS) Open(name string) (http.File, error) {
	f, err := fs.prepare(name)
	if err != nil {
		return nil, err
	}
	return f.File()
}

func (dir _escDirectory) Open(name string) (http.File, error) {
	return dir.fs.Open(dir.name + name)
}

func (f *_escFile) File() (http.File, error) {
	type httpFile struct {
		*bytes.Reader
		*_escFile
	}
	return &httpFile{
		Reader:   bytes.NewReader(f.data),
		_escFile: f,
	}, nil
}

func (f *_escFile) Close() error {
	return nil
}

func (f *_escFile) Readdir(count int) ([]os.FileInfo, error) {

	if !f.isDir  {
		return nil, nil
	}

	if err := f.prepare(); err != nil {
		return nil, err
	}

	prefix := "/"
	if len(f.local) > 1 {
		prefix = prefix + f.local + "/"
	}

	fis := make([]os.FileInfo, 0, len(_escData))

	for k, v := range _escData {
		if strings.HasPrefix(k, prefix) {
			if err := v.prepare(); err != nil {
				return fis, err
			}
			fis = append(fis, v)
		}
	}

	return fis, nil
}

func (f *_escFile) Stat() (os.FileInfo, error) {
	return f, nil
}

func (f *_escFile) Name() string {
	return f.name
}

func (f *_escFile) Size() int64 {
	return f.size
}

func (f *_escFile) Mode() os.FileMode {
	return 0
}

func (f *_escFile) ModTime() time.Time {
	return time.Unix(f.modtime, 0)
}

func (f *_escFile) IsDir() bool {
	return f.isDir
}

func (f *_escFile) Sys() interface{} {
	return f
}

// {{.FunctionPrefix}}FS returns a http.Filesystem for the embedded assets. If useLocal is true,
// the filesystem's contents are instead used.
func {{.FunctionPrefix}}FS(useLocal bool) http.FileSystem {
	if useLocal {
		return _escLocal
	}
	return _escStatic
}

// {{.FunctionPrefix}}Dir returns a http.Filesystem for the embedded assets on a given prefix dir.
// If useLocal is true, the filesystem's contents are instead used.
func {{.FunctionPrefix}}Dir(useLocal bool, name string) http.FileSystem {
	if useLocal {
		return _escDirectory{fs: _escLocal, name: name}
	}
	return _escDirectory{fs: _escStatic, name: name}
}

// {{.FunctionPrefix}}FSByte returns the named file from the embedded assets. If useLocal is
// true, the filesystem's contents are instead used.
func {{.FunctionPrefix}}FSByte(useLocal bool, name string) ([]byte, error) {
	if useLocal {
		f, err := _escLocal.Open(name)
		if err != nil {
			return nil, err
		}
		b, err := ioutil.ReadAll(f)
		_ = f.Close()
		return b, err
	}
	f, err := _escStatic.prepare(name)
	if err != nil {
		return nil, err
	}
	return f.data, nil
}

// {{.FunctionPrefix}}FSMustByte is the same as {{.FunctionPrefix}}FSByte, but panics if name is not present.
func {{.FunctionPrefix}}FSMustByte(useLocal bool, name string) []byte {
	b, err := {{.FunctionPrefix}}FSByte(useLocal, name)
	if err != nil {
		panic(err)
	}
	return b
}

// {{.FunctionPrefix}}FSString is the string version of {{.FunctionPrefix}}FSByte.
func {{.FunctionPrefix}}FSString(useLocal bool, name string) (string, error) {
	b, err := {{.FunctionPrefix}}FSByte(useLocal, name)
	return string(b), err
}

// {{.FunctionPrefix}}FSMustString is the string version of {{.FunctionPrefix}}FSMustByte.
func {{.FunctionPrefix}}FSMustString(useLocal bool, name string) string {
	return string({{.FunctionPrefix}}FSMustByte(useLocal, name))
}

var _escData = map[string]*_escFile{
`
	footer = `}
`
)
