package nativegen

import "strings"

// writeRuntimeTo appends the small set of helpers used by the emitted code.
// Each helper is gated on a `usesRuntime*` flag so unused helpers don't
// bloat the output (or trip Go's "declared and not used" errors). All
// helpers are package-private and prefixed `_tt_` so they can never collide
// with a user-derived name.
func (g *Generator) writeRuntimeTo(out *strings.Builder) {
	if !g.anyRuntimeUsed() {
		return
	}
	out.WriteString("\n// --- tartalo runtime ---\n\n")

	if g.usesRuntimePtr {
		out.WriteString("func _tt_ptr[T any](v T) *T { return &v }\n\n")
	}
	if g.usesRuntimeCoalesce {
		out.WriteString("func _tt_coalesce[T any](p *T, d T) T {\n")
		out.WriteString("\tif p == nil { return d }\n")
		out.WriteString("\treturn *p\n")
		out.WriteString("}\n\n")
	}
	if g.usesRuntimeUnwrap {
		out.WriteString("func _tt_unwrap[T any](p *T) T {\n")
		out.WriteString("\tif p == nil { panic(\"tartalo: unwrapped null\") }\n")
		out.WriteString("\treturn *p\n")
		out.WriteString("}\n\n")
	}
	if g.usesRuntimeTry {
		// _tt_tryErr is the sentinel a `?` operator panics with when its
		// operand is Err. The enclosing function recovers it and re-tags
		// its own Result-shaped return — see tryRecover injected into
		// every function declared as returning a sum.
		out.WriteString("type _tt_tryErr struct { err string }\n\n")
	}
	if g.usesRuntimeShellOut {
		out.WriteString(runtimeShellOut)
	}
	if g.usesRuntimeArgs {
		out.WriteString(runtimeArgs)
	}
	if g.usesRuntimeExec {
		out.WriteString(runtimeExec)
	}
	if g.usesRuntimeExecTimeout {
		out.WriteString(runtimeExecTimeout)
	}
	if g.usesRuntimeFile {
		out.WriteString(runtimeFile)
	}
	if g.usesRuntimeEnv {
		out.WriteString(runtimeEnv)
	}
	if g.usesRuntimeNow {
		out.WriteString(runtimeNow)
	}
	if g.usesRuntimePath {
		out.WriteString(runtimePath)
	}
	if g.usesRuntimeStat {
		out.WriteString(runtimeStat)
	}
	if g.usesRuntimeJSON {
		out.WriteString(runtimeJSON)
	}
	if g.usesRuntimeRegex {
		out.WriteString(runtimeRegex)
	}
	if g.usesRuntimeFormatTime {
		out.WriteString(runtimeFormatTime)
	}
	if g.usesRuntimeFloat {
		out.WriteString(runtimeFloat)
	}
	if g.usesRuntimeTypeError {
		out.WriteString(runtimeTypeError)
	}
	if g.usesRuntimeSpawn {
		out.WriteString(runtimeSpawn)
	}
	if g.usesRuntimeVec {
		out.WriteString(runtimeVec)
	}
	if g.usesRuntimeHigherOrder {
		out.WriteString(runtimeHigherOrder)
	}
	if g.usesRuntimeFetch {
		out.WriteString(runtimeFetch)
	}
	// The eval harness reuses `_tt_colors`, `_tt_testFailure`, and the
	// assertion helpers (`_tt_check` / `_tt_assertEq` / ...) from the test
	// harness, since `eval` bodies are allowed to call those. So whenever
	// either harness is in use, emit the test harness; the eval harness
	// adds the eval-only state and runner on top.
	if g.usesRuntimeTestState || g.usesRuntimeEvalState {
		out.WriteString(runtimeTestHarness)
	}
	if g.usesRuntimeEvalState {
		out.WriteString(runtimeEvalHarness)
	}
	g.writeMockableDispatchers(out)
	if g.emitMode == EmitTest || g.emitMode == EmitEval {
		out.WriteString(runtimeMockState)
		g.writeMockSetters(out)
	}
	g.emitCsvHelpers(out)
	g.emitAgentRuntimeAppendix(out)
}

// writeMockableDispatchers emits the public `_tt_<builtin>` function for
// every mockable builtin the program calls. In run mode these are one-line
// passthroughs to the `_real` impl. In test mode they consult the global
// `_tt_mock` state — for strict-mode builtins (exec/fetch/readFile) an
// unmatched call panics with `_tt_testFailure`; for fall-through builtins
// (env/now/args/readStdin) the dispatcher returns the override or falls
// back to the real impl.
func (g *Generator) writeMockableDispatchers(out *strings.Builder) {
	test := g.emitMode == EmitTest || g.emitMode == EmitEval
	if g.usesRuntimeExec {
		if test {
			out.WriteString(dispatcherExecTest)
		} else {
			out.WriteString(`func _tt_exec(cmd string) Tt_Process { return _tt_exec_real(cmd) }` + "\n\n")
		}
	}
	if g.usesRuntimeExecTimeout {
		if test {
			out.WriteString(dispatcherExecTimeoutTest)
		} else {
			out.WriteString(`func _tt_execTimeout(cmd string, secs int64) Tt_Process { return _tt_execTimeout_real(cmd, secs) }` + "\n\n")
		}
	}
	if g.usesRuntimeFetch {
		if test {
			out.WriteString(dispatcherFetchTest)
			out.WriteString(dispatcherFetchTimeoutTest)
			out.WriteString(dispatcherFetchHeadersTest)
			out.WriteString(dispatcherPostJsonTest)
			out.WriteString(dispatcherPostFormTest)
			out.WriteString(dispatcherRequestTest)
		} else {
			out.WriteString(`func _tt_fetch(url string) Tt_Response { return _tt_fetch_real(url) }` + "\n\n")
			out.WriteString(`func _tt_fetchTimeout(url string, secs int64) Tt_Response { return _tt_fetchTimeout_real(url, secs) }` + "\n\n")
			out.WriteString(`func _tt_fetchHeaders(url string, headers []string) Tt_Response { return _tt_fetchHeaders_real(url, headers) }` + "\n\n")
			out.WriteString(`func _tt_postJson(url, body string) Tt_Response { return _tt_postJson_real(url, body) }` + "\n\n")
			out.WriteString(`func _tt_postForm(url, body string) Tt_Response { return _tt_postForm_real(url, body) }` + "\n\n")
			out.WriteString(`func _tt_request(opts Tt_Request) Tt_Response { return _tt_request_real(opts) }` + "\n\n")
		}
	}
	if g.usesRuntimeFile {
		if test {
			out.WriteString(dispatcherReadFileTest)
			out.WriteString(dispatcherReadStdinTest)
		} else {
			out.WriteString(`func _tt_readFile(path string) string { return _tt_readFile_real(path) }` + "\n\n")
			out.WriteString(`func _tt_readStdin() string { return _tt_readStdin_real() }` + "\n\n")
		}
	}
	if g.usesRuntimeEnv {
		if test {
			out.WriteString(dispatcherEnvTest)
		} else {
			out.WriteString(`func _tt_env(name string) *string { return _tt_env_real(name) }` + "\n\n")
		}
	}
	if g.usesRuntimeNow {
		if test {
			out.WriteString(dispatcherNowTest)
		} else {
			out.WriteString(`func _tt_now() int64 { return _tt_now_real() }` + "\n\n")
		}
	}
	if g.usesRuntimeArgs {
		if test {
			out.WriteString(dispatcherArgsTest)
		} else {
			out.WriteString(`func _tt_args() []string { return _tt_args_real() }` + "\n\n")
		}
	}
}

// writeMockSetters emits the runtime helpers that `mockExec`, `mockEnv`,
// etc. lower to. Each is keyed off its `usesMockX` flag, mirroring how
// the rest of the runtime is gated. They never appear outside test mode.
func (g *Generator) writeMockSetters(out *strings.Builder) {
	if g.usesMockExec {
		out.WriteString(mockSettersExec)
	}
	if g.usesMockFetch {
		out.WriteString(mockSettersFetch)
	}
	if g.usesMockEnv {
		out.WriteString(mockSettersEnv)
	}
	if g.usesMockReadFile {
		out.WriteString(mockSettersReadFile)
	}
	if g.usesMockNow {
		out.WriteString(mockSettersNow)
	}
	if g.usesMockArgs {
		out.WriteString(mockSettersArgs)
	}
	if g.usesMockStdin {
		out.WriteString(mockSettersStdin)
	}
}

func (g *Generator) anyRuntimeUsed() bool {
	return g.usesRuntimeUnwrap || g.usesRuntimePtr || g.usesRuntimeCoalesce ||
		g.usesRuntimeShellOut || g.usesRuntimeArgs || g.usesRuntimeExec ||
		g.usesRuntimeExecTimeout || g.usesRuntimeFile || g.usesRuntimePath ||
		g.usesRuntimeStat || g.usesRuntimeJSON || g.usesRuntimeRegex ||
		g.usesRuntimeFormatTime || g.usesRuntimeFloat || g.usesRuntimeVec ||
		len(g.csvReaders) > 0 || len(g.csvWriters) > 0 ||
		g.usesRuntimeHigherOrder || g.usesRuntimeFetch || g.usesRuntimeTestState ||
		g.usesRuntimeEvalState ||
		g.usesRuntimeEnv || g.usesRuntimeNow || g.usesRuntimeTry ||
		g.usesRuntimeTypeError ||
		g.usesAgentLLM || g.usesAgentApproval || g.usesAgentTrace ||
		g.usesAgentSpawn ||
		g.usesRuntimeSpawn
}

const runtimeShellOut = `func _tt_shellOut(cmd string) string {
	var sh string
	var args []string
	if runtime.GOOS == "windows" {
		sh = "cmd"; args = []string{"/c", cmd}
	} else {
		sh = "/bin/sh"; args = []string{"-c", cmd}
	}
	out, _ := exec.Command(sh, args...).Output()
	return strings.TrimRight(string(out), "\n")
}

`

const runtimeArgs = `func _tt_args_real() []string {
	if len(os.Args) <= 1 { return []string{} }
	return os.Args[1:]
}

`

// runtimeEnv / runtimeNow / runtimeReadStdinShim hold the real implementations
// for the small builtins that used to be inlined as IIFEs in builtins.go. They
// were promoted to top-level helpers so the test-mode mock dispatcher has
// somewhere clean to fall through to.
const runtimeEnv = `func _tt_env_real(name string) *string {
	v, ok := os.LookupEnv(name)
	if !ok { return nil }
	return _tt_ptr(v)
}

`

const runtimeNow = `func _tt_now_real() int64 { return time.Now().Unix() }

`

const runtimeExec = `func _tt_exec_real(cmd string) Tt_Process {
	var shBin string
	var shArgs []string
	if runtime.GOOS == "windows" {
		shBin = "cmd"; shArgs = []string{"/c", cmd}
	} else {
		shBin = "/bin/sh"; shArgs = []string{"-c", cmd}
	}
	c := exec.Command(shBin, shArgs...)
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr
	err := c.Run()
	code := int64(0)
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = int64(ee.ExitCode())
		} else {
			code = 1
		}
	}
	return Tt_Process{F_code: code, F_ok: code == 0, F_stdout: stdout.String(), F_stderr: stderr.String()}
}

`

const runtimeExecTimeout = `func _tt_execTimeout_real(cmd string, secs int64) Tt_Process {
	var shBin string
	var shArgs []string
	if runtime.GOOS == "windows" {
		shBin = "cmd"; shArgs = []string{"/c", cmd}
	} else {
		shBin = "/bin/sh"; shArgs = []string{"-c", cmd}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(secs)*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, shBin, shArgs...)
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr
	err := c.Run()
	code := int64(0)
	if ctx.Err() == context.DeadlineExceeded {
		code = 124
	} else if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = int64(ee.ExitCode())
		} else {
			code = 1
		}
	}
	return Tt_Process{F_code: code, F_ok: code == 0, F_stdout: stdout.String(), F_stderr: stderr.String()}
}

`

const runtimeFile = `func _tt_readFile_real(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tartalo: readFile: cannot read %s\n", path)
		os.Exit(1)
	}
	return strings.TrimRight(string(b), "\n")
}

func _tt_writeFile(path, content string) {
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "tartalo: writeFile: cannot write %s\n", path)
		os.Exit(1)
	}
}

func _tt_appendFile(path, content string) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tartalo: appendFile: cannot write %s\n", path)
		os.Exit(1)
	}
	defer f.Close()
	if _, err := f.WriteString(content); err != nil {
		fmt.Fprintf(os.Stderr, "tartalo: appendFile: cannot write %s\n", path)
		os.Exit(1)
	}
}

func _tt_listDir(path string) []string {
	entries, err := os.ReadDir(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tartalo: listDir: cannot list %s\n", path)
		os.Exit(1)
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func _tt_readStdin_real() string {
	b, _ := io.ReadAll(os.Stdin)
	return strings.TrimRight(string(b), "\n")
}

`

const runtimePath = `// _tt_pathJoin matches Tartalo's sh-backend semantics: an absolute second
// argument wins; otherwise we splice with a single '/' separator regardless
// of trailing slashes in the first argument.
func _tt_pathJoin(a, b string) string {
	if strings.HasPrefix(b, "/") {
		return b
	}
	if strings.HasSuffix(a, "/") {
		return a + b
	}
	return a + "/" + b
}

func _tt_extname(path string) string {
	base := filepath.Base(path)
	i := strings.LastIndexByte(base, '.')
	if i < 0 {
		return ""
	}
	return base[i:]
}

func _tt_parsePath(path string) Tt_PathParts {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	ext := _tt_extname(path)
	name := base
	if ext != "" {
		name = base[:len(base)-len(ext)]
	}
	return Tt_PathParts{F_dir: dir, F_base: base, F_name: name, F_ext: ext}
}

`

const runtimeStat = `func _tt_stat(path string) Tt_FileInfo {
	info, err := os.Stat(path)
	if err != nil {
		return Tt_FileInfo{}
	}
	return Tt_FileInfo{
		F_exists: true,
		F_isFile: info.Mode().IsRegular(),
		F_isDir:  info.IsDir(),
		F_size:   info.Size(),
		F_mtime:  info.ModTime().Unix(),
		F_mode:   strconv.FormatUint(uint64(info.Mode().Perm()), 8),
	}
}

`

const runtimeJSON = `// _tt_jsonGet walks a jq-style path (".a.b" or ".a[0]" or ".") and returns
// the leaf value as a string, or nil if the path is missing or null. We
// support the subset of jq paths used by Tartalo programs: dotted field
// access and integer array indexing. Anything else falls back to nil.
func _tt_jsonGet(body, path string) *string {
	v, ok := _tt_jsonResolve(body, path)
	if !ok || v == nil {
		return nil
	}
	switch t := v.(type) {
	case string:
		return _tt_ptr(t)
	case bool:
		if t { s := "true"; return &s }
		s := "false"; return &s
	case float64:
		// Numbers come back as float64 from encoding/json. Render integer-
		// valued numbers without a decimal to match jq's -r output.
		if t == float64(int64(t)) {
			s := strconv.FormatInt(int64(t), 10); return &s
		}
		s := strconv.FormatFloat(t, 'g', -1, 64); return &s
	default:
		// Object / array: marshal back to JSON. jq -r prints these as JSON.
		b, _ := json.Marshal(t)
		s := string(b); return &s
	}
}

func _tt_jsonHas(body, path string) bool {
	v, ok := _tt_jsonResolve(body, path)
	return ok && v != nil
}

func _tt_jsonArray(body, path string) []string {
	v, ok := _tt_jsonResolve(body, path)
	if !ok {
		return []string{}
	}
	arr, ok := v.([]interface{})
	if !ok {
		return []string{}
	}
	out := make([]string, 0, len(arr))
	for _, el := range arr {
		switch t := el.(type) {
		case string:
			out = append(out, t)
		case bool:
			if t { out = append(out, "true") } else { out = append(out, "false") }
		case float64:
			if t == float64(int64(t)) {
				out = append(out, strconv.FormatInt(int64(t), 10))
			} else {
				out = append(out, strconv.FormatFloat(t, 'g', -1, 64))
			}
		default:
			b, _ := json.Marshal(t)
			out = append(out, string(b))
		}
	}
	return out
}

func _tt_jsonEscape(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func _tt_jsonResolve(body, path string) (interface{}, bool) {
	var v interface{}
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		return nil, false
	}
	if path == "." || path == "" {
		return v, true
	}
	p := strings.TrimPrefix(path, ".")
	for p != "" {
		// Either a [N] index or a name terminated by '.' or '['.
		if p[0] == '[' {
			end := strings.IndexByte(p, ']')
			if end < 0 { return nil, false }
			idx, err := strconv.Atoi(p[1:end])
			if err != nil { return nil, false }
			arr, ok := v.([]interface{})
			if !ok || idx < 0 || idx >= len(arr) { return nil, false }
			v = arr[idx]
			p = p[end+1:]
			if strings.HasPrefix(p, ".") { p = p[1:] }
			continue
		}
		// Field name ends at '.' or '['.
		end := len(p)
		for i := 0; i < len(p); i++ {
			if p[i] == '.' || p[i] == '[' { end = i; break }
		}
		key := p[:end]
		obj, ok := v.(map[string]interface{})
		if !ok { return nil, false }
		next, exists := obj[key]
		if !exists { return nil, false }
		v = next
		p = p[end:]
		if strings.HasPrefix(p, ".") { p = p[1:] }
	}
	return v, true
}

`

const runtimeRegex = `// Tartalo's sh backend runs POSIX ERE through awk. Go's regexp uses RE2
// (no backrefs, otherwise a near-superset). For the common patterns used by
// Tartalo programs the two agree; documented divergences are listed in the
// SPEC. Compilation panics on a malformed pattern so failures surface
// immediately at first use rather than silently returning empty.
func _tt_regexMatch(s, pat string) bool {
	re := regexp.MustCompile(pat)
	return re.MatchString(s)
}

func _tt_regexFind(s, pat string) *string {
	re := regexp.MustCompile(pat)
	loc := re.FindStringIndex(s)
	if loc == nil {
		return nil
	}
	out := s[loc[0]:loc[1]]
	return &out
}

func _tt_regexFindAll(s, pat string) []string {
	re := regexp.MustCompile(pat)
	hits := re.FindAllString(s, -1)
	if hits == nil {
		return []string{}
	}
	return hits
}

func _tt_regexReplace(s, pat, rep string) string {
	re := regexp.MustCompile(pat)
	// Match the sh backend's literal-replacement semantics: backslashes and
	// '$' aren't special. Quote them so Go's $-substitutions don't fire.
	rep = strings.NewReplacer("$", "$$", "\\", "\\\\").Replace(rep)
	return re.ReplaceAllString(s, rep)
}

`

const runtimeFormatTime = `// _tt_formatTime maps a strftime-style format string to Go's time.Format.
// Only the tokens used by Tartalo example programs are translated — the
// rest pass through verbatim, which is also what the sh backend does on a
// best-effort basis.
func _tt_formatTime(secs int64, fmtStr string) string {
	t := time.Unix(secs, 0)
	tokens := []struct{ from, to string }{
		{"%Y", "2006"}, {"%y", "06"},
		{"%m", "01"}, {"%d", "02"},
		{"%H", "15"}, {"%M", "04"}, {"%S", "05"},
		{"%B", "January"}, {"%b", "Jan"},
		{"%A", "Monday"}, {"%a", "Mon"},
		{"%Z", "MST"}, {"%z", "-0700"},
		{"%%", "%"},
	}
	out := fmtStr
	for _, tk := range tokens {
		out = strings.ReplaceAll(out, tk.from, tk.to)
	}
	return t.Format(out)
}

`

const runtimeHigherOrder = `func _tt_map[T, U any](xs []T, f func(T) U) []U {
	out := make([]U, len(xs))
	for i, x := range xs { out[i] = f(x) }
	return out
}

func _tt_filter[T any](xs []T, f func(T) bool) []T {
	out := make([]T, 0, len(xs))
	for _, x := range xs { if f(x) { out = append(out, x) } }
	return out
}

func _tt_reduce[T, U any](xs []T, init U, f func(U, T) U) U {
	acc := init
	for _, x := range xs { acc = f(acc, x) }
	return acc
}

`

const runtimeFetch = `// _tt_request_real is the shared engine: every fetch-family builtin
// (fetch, fetchTimeout, fetchHeaders, postJson, postForm, request) ends
// up here. Transport / DNS errors surface as ok=false / status=0 so the
// caller can branch on r.ok or r.status == 0 the same way they would
// against the sh backend's curl failure behaviour. The default 30s
// timeout prevents binaries from hanging indefinitely; an opts.timeout
// of 0 keeps that default.
func _tt_request_real(opts Tt_Request) Tt_Response {
	timeout := time.Duration(opts.F_timeout) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	client := &http.Client{Timeout: timeout}
	if !opts.F_followRedirects {
		client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		}
	}
	if opts.F_insecure {
		client.Transport = &http.Transport{TLSClientConfig: _tt_insecureTLSConfig()}
	}
	method := opts.F_method
	if method == "" {
		method = "GET"
	}
	var bodyReader io.Reader
	if opts.F_body != "" {
		bodyReader = strings.NewReader(opts.F_body)
	}
	req, err := http.NewRequest(method, opts.F_url, bodyReader)
	if err != nil {
		return Tt_Response{F_status: 0, F_ok: false}
	}
	for _, h := range opts.F_headers {
		idx := strings.Index(h, ":")
		if idx < 0 {
			continue
		}
		name := strings.TrimSpace(h[:idx])
		value := strings.TrimSpace(h[idx+1:])
		if name == "" {
			continue
		}
		req.Header.Add(name, value)
	}
	if opts.F_user != "" {
		req.SetBasicAuth(opts.F_user, opts.F_password)
	}
	resp, err := client.Do(req)
	if err != nil {
		return Tt_Response{F_status: 0, F_ok: false}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var hdrs strings.Builder
	hdrs.WriteString(resp.Proto + " " + resp.Status + "\r\n")
	for k, vs := range resp.Header {
		for _, v := range vs {
			hdrs.WriteString(k + ": " + v + "\r\n")
		}
	}
	return Tt_Response{
		F_status:  int64(resp.StatusCode),
		F_ok:      resp.StatusCode >= 200 && resp.StatusCode < 300,
		F_body:    string(body),
		F_headers: hdrs.String(),
	}
}

func _tt_fetch_real(url string) Tt_Response {
	return _tt_request_real(Tt_Request{F_url: url, F_method: "GET", F_followRedirects: true})
}

func _tt_fetchTimeout_real(url string, secs int64) Tt_Response {
	return _tt_request_real(Tt_Request{F_url: url, F_method: "GET", F_timeout: secs, F_followRedirects: true})
}

func _tt_fetchHeaders_real(url string, headers []string) Tt_Response {
	return _tt_request_real(Tt_Request{F_url: url, F_method: "GET", F_headers: headers, F_followRedirects: true})
}

func _tt_postJson_real(url, body string) Tt_Response {
	return _tt_request_real(Tt_Request{
		F_url: url, F_method: "POST", F_body: body,
		F_headers:         []string{"Content-Type: application/json"},
		F_followRedirects: true,
	})
}

func _tt_postForm_real(url, body string) Tt_Response {
	return _tt_request_real(Tt_Request{
		F_url: url, F_method: "POST", F_body: body,
		F_headers:         []string{"Content-Type: application/x-www-form-urlencoded"},
		F_followRedirects: true,
	})
}

// _tt_header walks a curl-style raw headers blob and returns the first
// matching value (case-insensitive). Returns nil for missing so the call
// site can model the result as ` + "`string?`" + ` and use ?? / ! naturally.
func _tt_header(r Tt_Response, name string) *string {
	want := strings.ToLower(strings.TrimSpace(name))
	for _, line := range strings.Split(r.F_headers, "\n") {
		line = strings.TrimRight(line, "\r")
		idx := strings.Index(line, ":")
		if idx < 0 {
			continue
		}
		if strings.ToLower(strings.TrimSpace(line[:idx])) != want {
			continue
		}
		v := strings.TrimSpace(line[idx+1:])
		return &v
	}
	return nil
}

// _tt_urlEncode percent-encodes per RFC 3986's "unreserved" set
// (ALPHA / DIGIT / - . _ ~). Other bytes become %HH. Mirrors the awk
// helper in the sh backend so cross-target output is byte-identical.
func _tt_urlEncode(s string) string {
	const safe = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~"
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if strings.IndexByte(safe, c) >= 0 {
			b.WriteByte(c)
		} else {
			const hex = "0123456789ABCDEF"
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&0x0F])
		}
	}
	return b.String()
}

func _tt_insecureTLSConfig() *tls.Config {
	return &tls.Config{InsecureSkipVerify: true}
}

`

// runtimeMockState declares the `_tt_mock` package-level singleton holding
// the current test's mock registrations and call records. The harness's
// per-test wrapper resets it between tests so each test starts with a
// clean slate. Always emitted in test mode (whether or not any specific
// mock is registered) so the dispatchers have somewhere to look.
const runtimeMockState = `type _tt_mockExecRule struct {
	pat  *regexp.Regexp
	resp Tt_Process
}

type _tt_mockFetchRule struct {
	pat  *regexp.Regexp
	resp Tt_Response
}

type _tt_mockReadFileRule struct {
	pat     *regexp.Regexp
	content string
}

type _tt_mockStateT struct {
	execRules     []_tt_mockExecRule
	execCalls     []string
	fetchRules    []_tt_mockFetchRule
	fetchCalls    []string
	readFileRules []_tt_mockReadFileRule
	readFileCalls []string
	envOverrides  map[string]*string
	envHas        bool
	nowFrozen     *int64
	argsOverride  *[]string
	stdinOverride *string
}

var _tt_mock _tt_mockStateT

// _tt_mockResetHooks lets per-feature mock modules register their own
// reset routines (e.g., the LLM mock state lives in the agent appendix
// and only exists when llm() is used). Each hook is called between
// tests, after the core _tt_mock state is cleared.
var _tt_mockResetHooks []func()

func _tt_mock_reset() {
	_tt_mock = _tt_mockStateT{}
	for _, h := range _tt_mockResetHooks {
		h()
	}
}

`

// Dispatchers — each consults the relevant slice of `_tt_mock` first.
// strict-mode builtins (exec/fetch/readFile) panic with `_tt_testFailure`
// when at least one rule is registered but none matches. Fall-through
// builtins (env/now/args/readStdin) return the override if any, otherwise
// call the real impl unchanged.

const dispatcherExecTest = `func _tt_exec(cmd string) Tt_Process {
	_tt_mock.execCalls = append(_tt_mock.execCalls, cmd)
	if len(_tt_mock.execRules) > 0 {
		for _, r := range _tt_mock.execRules {
			if r.pat.MatchString(cmd) {
				return r.resp
			}
		}
		panic(_tt_testFailure{msg: "exec: no mock matched: " + cmd})
	}
	return _tt_exec_real(cmd)
}

`

const dispatcherExecTimeoutTest = `func _tt_execTimeout(cmd string, secs int64) Tt_Process {
	_tt_mock.execCalls = append(_tt_mock.execCalls, cmd)
	if len(_tt_mock.execRules) > 0 {
		for _, r := range _tt_mock.execRules {
			if r.pat.MatchString(cmd) {
				return r.resp
			}
		}
		panic(_tt_testFailure{msg: "execTimeout: no mock matched: " + cmd})
	}
	return _tt_execTimeout_real(cmd, secs)
}

`

const dispatcherFetchTest = `func _tt_fetch(url string) Tt_Response {
	_tt_mock.fetchCalls = append(_tt_mock.fetchCalls, url)
	if len(_tt_mock.fetchRules) > 0 {
		for _, r := range _tt_mock.fetchRules {
			if r.pat.MatchString(url) {
				return r.resp
			}
		}
		panic(_tt_testFailure{msg: "fetch: no mock matched: " + url})
	}
	return _tt_fetch_real(url)
}

`

// The remaining fetch-family builtins funnel through the same mock
// store. mockFetch matches against the request URL so a single rule
// covers fetch / fetchTimeout / fetchHeaders / postJson / postForm /
// request alike — what the SUT did with the URL is its business; the
// test only cares that the URL got hit.

const dispatcherFetchTimeoutTest = `func _tt_fetchTimeout(url string, secs int64) Tt_Response {
	_tt_mock.fetchCalls = append(_tt_mock.fetchCalls, url)
	if len(_tt_mock.fetchRules) > 0 {
		for _, r := range _tt_mock.fetchRules {
			if r.pat.MatchString(url) {
				return r.resp
			}
		}
		panic(_tt_testFailure{msg: "fetchTimeout: no mock matched: " + url})
	}
	return _tt_fetchTimeout_real(url, secs)
}

`

const dispatcherFetchHeadersTest = `func _tt_fetchHeaders(url string, headers []string) Tt_Response {
	_tt_mock.fetchCalls = append(_tt_mock.fetchCalls, url)
	if len(_tt_mock.fetchRules) > 0 {
		for _, r := range _tt_mock.fetchRules {
			if r.pat.MatchString(url) {
				return r.resp
			}
		}
		panic(_tt_testFailure{msg: "fetchHeaders: no mock matched: " + url})
	}
	return _tt_fetchHeaders_real(url, headers)
}

`

const dispatcherPostJsonTest = `func _tt_postJson(url, body string) Tt_Response {
	_tt_mock.fetchCalls = append(_tt_mock.fetchCalls, url)
	if len(_tt_mock.fetchRules) > 0 {
		for _, r := range _tt_mock.fetchRules {
			if r.pat.MatchString(url) {
				return r.resp
			}
		}
		panic(_tt_testFailure{msg: "postJson: no mock matched: " + url})
	}
	return _tt_postJson_real(url, body)
}

`

const dispatcherPostFormTest = `func _tt_postForm(url, body string) Tt_Response {
	_tt_mock.fetchCalls = append(_tt_mock.fetchCalls, url)
	if len(_tt_mock.fetchRules) > 0 {
		for _, r := range _tt_mock.fetchRules {
			if r.pat.MatchString(url) {
				return r.resp
			}
		}
		panic(_tt_testFailure{msg: "postForm: no mock matched: " + url})
	}
	return _tt_postForm_real(url, body)
}

`

const dispatcherRequestTest = `func _tt_request(opts Tt_Request) Tt_Response {
	_tt_mock.fetchCalls = append(_tt_mock.fetchCalls, opts.F_url)
	if len(_tt_mock.fetchRules) > 0 {
		for _, r := range _tt_mock.fetchRules {
			if r.pat.MatchString(opts.F_url) {
				return r.resp
			}
		}
		panic(_tt_testFailure{msg: "request: no mock matched: " + opts.F_url})
	}
	return _tt_request_real(opts)
}

`

const dispatcherReadFileTest = `func _tt_readFile(path string) string {
	_tt_mock.readFileCalls = append(_tt_mock.readFileCalls, path)
	if len(_tt_mock.readFileRules) > 0 {
		for _, r := range _tt_mock.readFileRules {
			if r.pat.MatchString(path) {
				return r.content
			}
		}
		panic(_tt_testFailure{msg: "readFile: no mock matched: " + path})
	}
	return _tt_readFile_real(path)
}

`

const dispatcherReadStdinTest = `func _tt_readStdin() string {
	if _tt_mock.stdinOverride != nil {
		return *_tt_mock.stdinOverride
	}
	return _tt_readStdin_real()
}

`

const dispatcherEnvTest = `func _tt_env(name string) *string {
	if _tt_mock.envHas {
		if v, ok := _tt_mock.envOverrides[name]; ok {
			return v
		}
	}
	return _tt_env_real(name)
}

`

const dispatcherNowTest = `func _tt_now() int64 {
	if _tt_mock.nowFrozen != nil {
		return *_tt_mock.nowFrozen
	}
	return _tt_now_real()
}

`

const dispatcherArgsTest = `func _tt_args() []string {
	if _tt_mock.argsOverride != nil {
		return *_tt_mock.argsOverride
	}
	return _tt_args_real()
}

`

// Mock setters / inspectors — emitted only when the program references the
// matching mock builtin. Pattern compilation happens at registration so a
// bad regex blows up with a clear `regexp: Compile` error at the call site,
// not silently inside the dispatcher.

const mockSettersExec = `func _tt_mockExec(pat string, resp Tt_Process) {
	_tt_mock.execRules = append(_tt_mock.execRules, _tt_mockExecRule{pat: regexp.MustCompile(pat), resp: resp})
}

func _tt_mockExecCalls() []string {
	out := make([]string, len(_tt_mock.execCalls))
	copy(out, _tt_mock.execCalls)
	return out
}

`

const mockSettersFetch = `func _tt_mockFetch(pat string, resp Tt_Response) {
	_tt_mock.fetchRules = append(_tt_mock.fetchRules, _tt_mockFetchRule{pat: regexp.MustCompile(pat), resp: resp})
}

func _tt_mockFetchCalls() []string {
	out := make([]string, len(_tt_mock.fetchCalls))
	copy(out, _tt_mock.fetchCalls)
	return out
}

`

const mockSettersEnv = `func _tt_mockEnv(name string, value *string) {
	if _tt_mock.envOverrides == nil {
		_tt_mock.envOverrides = map[string]*string{}
	}
	_tt_mock.envOverrides[name] = value
	_tt_mock.envHas = true
}

`

const mockSettersReadFile = `func _tt_mockReadFile(pat, content string) {
	_tt_mock.readFileRules = append(_tt_mock.readFileRules, _tt_mockReadFileRule{pat: regexp.MustCompile(pat), content: content})
}

func _tt_mockReadFileCalls() []string {
	out := make([]string, len(_tt_mock.readFileCalls))
	copy(out, _tt_mock.readFileCalls)
	return out
}

`

const mockSettersNow = `func _tt_mockNow(secs int64) {
	v := secs
	_tt_mock.nowFrozen = &v
}

`

const mockSettersArgs = `func _tt_mockArgs(xs []string) {
	v := make([]string, len(xs))
	copy(v, xs)
	_tt_mock.argsOverride = &v
}

`

const mockSettersStdin = `func _tt_mockReadStdin(s string) {
	v := s
	_tt_mock.stdinOverride = &v
}

`

const runtimeTestHarness = `// _tt_testFailure / _tt_testSkip are sentinel panic types the harness
// recovers per-test. assertions panic to abort the current test cleanly
// while leaving the rest of the suite running.
type _tt_testFailure struct{ msg string }
type _tt_testSkip struct{ reason string }

func _tt_render(v interface{}) string {
	if b, ok := v.(bool); ok {
		if b { return "1" }
		return "0"
	}
	return fmt.Sprintf("%v", v)
}

func _tt_assertEq(a, b interface{}, loc string) {
	if a != b {
		panic(_tt_testFailure{msg: "assertEq failed at " + loc + ":\n  expected: " + _tt_render(b) + "\n  actual:   " + _tt_render(a)})
	}
}

func _tt_assertNe(a, b interface{}, loc string) {
	if a == b {
		panic(_tt_testFailure{msg: "assertNe failed at " + loc + ":\n  but got equal: " + _tt_render(b) + "\n  actual:        " + _tt_render(a)})
	}
}

func _tt_check(b bool, loc string) {
	if !b {
		panic(_tt_testFailure{msg: "check failed at " + loc})
	}
}

func _tt_fail(msg, loc string) {
	panic(_tt_testFailure{msg: "fail at " + loc + ":\n  " + msg})
}

func _tt_skip(reason string) {
	panic(_tt_testSkip{reason: reason})
}

type _tt_testCase struct {
	name string
	fn   func()
}

func _tt_colors() (pass, fail, skip, dim, bold, off string) {
	if os.Getenv("NO_COLOR") != "" {
		return
	}
	if fi, err := os.Stdout.Stat(); err != nil || (fi.Mode()&os.ModeCharDevice) == 0 {
		return
	}
	return "\x1b[32m", "\x1b[31m", "\x1b[33m", "\x1b[2m", "\x1b[1m", "\x1b[0m"
}

func _tt_runTests(suite string, tests []_tt_testCase) {
	cPass, cFail, cSkip, cDim, cBold, cOff := _tt_colors()
	fmt.Printf("%srunning %d test(s) in %s%s\n\n", cDim, len(tests), suite, cOff)
	passed, failed, skipped := 0, 0, 0
	for _, tc := range tests {
		var skipReason string
		var failMsg string
		func() {
			_tt_mock_reset()
			defer func() {
				if r := recover(); r != nil {
					switch e := r.(type) {
					case _tt_testFailure:
						failMsg = e.msg
					case _tt_testSkip:
						skipReason = e.reason
					default:
						failMsg = fmt.Sprintf("panic: %v", r)
					}
				}
			}()
			tc.fn()
		}()
		if skipReason != "" {
			skipped++
			fmt.Printf("  %s-%s %s %s(skipped: %s)%s\n", cSkip, cOff, tc.name, cDim, skipReason, cOff)
			continue
		}
		if failMsg == "" {
			passed++
			fmt.Printf("  %s✓%s %s\n", cPass, cOff, tc.name)
			continue
		}
		failed++
		fmt.Printf("  %s✗%s %s%s%s\n", cFail, cOff, cBold, tc.name, cOff)
		for _, line := range strings.Split(failMsg, "\n") {
			fmt.Printf("      %s\n", line)
		}
	}
	fmt.Printf("\n")
	if failed == 0 {
		fmt.Printf("%s%d passed%s", cPass, passed, cOff)
	} else {
		fmt.Printf("%s%d failed%s, %d passed", cFail, failed, cOff, passed)
	}
	if skipped > 0 {
		fmt.Printf(", %s%d skipped%s", cSkip, skipped, cOff)
	}
	fmt.Printf(" (%d total)\n", len(tests))
	if failed > 0 {
		os.Exit(1)
	}
}

`

// runtimeTypeError is the shared abort helper for boundary type assertions
// (asInt / asFloat / asBool). Mirrors the sh backend's __tartalo_typeerror:
// prints `tartalo: type error at FILE:LINE:COL: expected EXPECTED, got GOT`
// to stderr and exits 1.
const runtimeTypeError = `func _tt_typeError(loc, expected, got string) {
	fmt.Fprintf(os.Stderr, "tartalo: type error at %s: expected %s, got %s\n", loc, expected, got)
	os.Exit(1)
}

`

// runtimeSpawn defines the global WaitGroup that spawn statements add to
// and waitAll() blocks on, plus a tiny helper to package "Add(1)+go+Done"
// in one call. Channels themselves don't need a runtime helper — they're
// just Go's `chan T`.
const runtimeSpawn = `var _tt_spawn_wg sync.WaitGroup

func _tt_spawn(fn func()) {
	_tt_spawn_wg.Add(1)
	go func() {
		defer _tt_spawn_wg.Done()
		fn()
	}()
}

`

const runtimeFloat = `func _tt_floatOf(n int64) float64 { return float64(n) }
func _tt_intOf(f float64) int64 { return int64(f) }

func _tt_parseFloat(s string) *float64 {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return nil
	}
	return &f
}

func _tt_formatFloat(f float64, prec int64) string {
	return strconv.FormatFloat(f, 'f', int(prec), 64)
}

func _tt_floor(f float64) float64 { return math.Floor(f) }
func _tt_ceil(f float64) float64  { return math.Ceil(f) }
func _tt_round(f float64) float64 { return math.Round(f) }

`

const runtimeVec = `func _tt_vSum(xs []float64) float64 {
	s := 0.0
	for _, x := range xs {
		s += x
	}
	return s
}

func _tt_vMean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	return _tt_vSum(xs) / float64(len(xs))
}

func _tt_vMin(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	m := xs[0]
	for _, x := range xs[1:] {
		if x < m {
			m = x
		}
	}
	return m
}

func _tt_vMax(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	m := xs[0]
	for _, x := range xs[1:] {
		if x > m {
			m = x
		}
	}
	return m
}

func _tt_vVar(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	mu := _tt_vMean(xs)
	s := 0.0
	for _, x := range xs {
		d := x - mu
		s += d * d
	}
	return s / float64(len(xs))
}

func _tt_vStd(xs []float64) float64 { return math.Sqrt(_tt_vVar(xs)) }

func _tt_vAdd(a, b []float64) []float64 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	out := make([]float64, n)
	for i := 0; i < n; i++ {
		out[i] = a[i] + b[i]
	}
	return out
}

func _tt_vSub(a, b []float64) []float64 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	out := make([]float64, n)
	for i := 0; i < n; i++ {
		out[i] = a[i] - b[i]
	}
	return out
}

func _tt_vMul(a, b []float64) []float64 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	out := make([]float64, n)
	for i := 0; i < n; i++ {
		out[i] = a[i] * b[i]
	}
	return out
}

func _tt_vScale(xs []float64, k float64) []float64 {
	out := make([]float64, len(xs))
	for i, x := range xs {
		out[i] = x * k
	}
	return out
}

func _tt_vDot(a, b []float64) float64 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	s := 0.0
	for i := 0; i < n; i++ {
		s += a[i] * b[i]
	}
	return s
}

func _tt_linspace(start, end float64, n int64) []float64 {
	if n <= 0 {
		return []float64{}
	}
	if n == 1 {
		return []float64{start}
	}
	out := make([]float64, n)
	step := (end - start) / float64(n-1)
	for i := int64(0); i < n; i++ {
		out[i] = start + float64(i)*step
	}
	return out
}

func _tt_arange(start, end, step int64) []int64 {
	if step == 0 {
		return []int64{}
	}
	var out []int64
	if step > 0 {
		for i := start; i < end; i += step {
			out = append(out, i)
		}
	} else {
		for i := start; i > end; i += step {
			out = append(out, i)
		}
	}
	return out
}

func _tt_cumsum(xs []float64) []float64 {
	out := make([]float64, len(xs))
	s := 0.0
	for i, x := range xs {
		s += x
		out[i] = s
	}
	return out
}

`
