package agentcfg

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// jsonedit：保留注释与格式的 JSON / JSONC 字节级编辑器。
//
// 为什么不用 encoding/json 整体读写：用户的配置里常有注释、尾逗号和自己的缩进习惯，
// 整体重写会把这些全部抹掉。这里只定位要改的那一段文本做替换，其余字节原样保留。
//
// 支持：// 与 /* */ 注释、尾逗号、UTF-8 BOM、CRLF。不支持 JSON5 的单引号与无引号键，
// 遇到时解析失败并拒绝写入，宁可不改也不改坏。

type jsonNode struct {
	kind       byte // '{' '[' 's' 'v'（数字 / true / false / null）
	start, end int  // 值文本的 [start, end)
	members    []jsonMember
	elems      []*jsonNode
}

type jsonMember struct {
	key              string
	keyStart, keyEnd int
	val              *jsonNode
}

type jsonDoc struct {
	src  string
	root *jsonNode
	eol  string
	unit string
}

type jscanner struct {
	s string
	i int
}

func (p *jscanner) errf(format string, a ...any) error {
	line, col := 1, 1
	for k := 0; k < p.i && k < len(p.s); k++ {
		if p.s[k] == '\n' {
			line++
			col = 1
		} else {
			col++
		}
	}
	return fmt.Errorf("JSON 解析失败（第 %d 行第 %d 列）：%s", line, col, fmt.Sprintf(format, a...))
}

// skip 跳过空白与注释。
func (p *jscanner) skip() error {
	for p.i < len(p.s) {
		switch c := p.s[p.i]; {
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			p.i++
		case c == '/' && p.i+1 < len(p.s) && p.s[p.i+1] == '/':
			for p.i < len(p.s) && p.s[p.i] != '\n' {
				p.i++
			}
		case c == '/' && p.i+1 < len(p.s) && p.s[p.i+1] == '*':
			end := strings.Index(p.s[p.i+2:], "*/")
			if end < 0 {
				return p.errf("块注释没有结束")
			}
			p.i += 2 + end + 2
		default:
			return nil
		}
	}
	return nil
}

func (p *jscanner) str() (int, int, error) {
	start := p.i
	p.i++
	for p.i < len(p.s) {
		switch p.s[p.i] {
		case '\\':
			p.i += 2
		case '"':
			p.i++
			return start, p.i, nil
		case '\n':
			return 0, 0, p.errf("字符串中出现换行")
		default:
			p.i++
		}
	}
	return 0, 0, p.errf("字符串没有结束")
}

func (p *jscanner) value() (*jsonNode, error) {
	if err := p.skip(); err != nil {
		return nil, err
	}
	if p.i >= len(p.s) {
		return nil, p.errf("意外的结尾")
	}
	switch c := p.s[p.i]; c {
	case '{':
		return p.object()
	case '[':
		return p.array()
	case '"':
		s, e, err := p.str()
		if err != nil {
			return nil, err
		}
		return &jsonNode{kind: 's', start: s, end: e}, nil
	default:
		s := p.i
		for p.i < len(p.s) && !strings.ContainsRune(",}] \t\r\n/", rune(p.s[p.i])) {
			p.i++
		}
		if p.i == s {
			return nil, p.errf("无法识别的字符 %q", string(c))
		}
		tok := p.s[s:p.i]
		var probe any
		if err := json.Unmarshal([]byte(tok), &probe); err != nil {
			return nil, p.errf("无效的值 %q（不支持 JSON5 写法）", tok)
		}
		return &jsonNode{kind: 'v', start: s, end: p.i}, nil
	}
}

func (p *jscanner) object() (*jsonNode, error) {
	n := &jsonNode{kind: '{', start: p.i}
	p.i++
	for {
		if err := p.skip(); err != nil {
			return nil, err
		}
		if p.i >= len(p.s) {
			return nil, p.errf("对象没有结束")
		}
		if p.s[p.i] == '}' {
			p.i++
			n.end = p.i
			return n, nil
		}
		if p.s[p.i] != '"' {
			return nil, p.errf("对象的键必须是双引号字符串")
		}
		ks, ke, err := p.str()
		if err != nil {
			return nil, err
		}
		var key string
		if err := json.Unmarshal([]byte(p.s[ks:ke]), &key); err != nil {
			return nil, p.errf("无效的键")
		}
		if err := p.skip(); err != nil {
			return nil, err
		}
		if p.i >= len(p.s) || p.s[p.i] != ':' {
			return nil, p.errf("缺少冒号")
		}
		p.i++
		v, err := p.value()
		if err != nil {
			return nil, err
		}
		n.members = append(n.members, jsonMember{key: key, keyStart: ks, keyEnd: ke, val: v})
		if err := p.skip(); err != nil {
			return nil, err
		}
		if p.i < len(p.s) && p.s[p.i] == ',' {
			p.i++
		}
	}
}

func (p *jscanner) array() (*jsonNode, error) {
	n := &jsonNode{kind: '[', start: p.i}
	p.i++
	for {
		if err := p.skip(); err != nil {
			return nil, err
		}
		if p.i >= len(p.s) {
			return nil, p.errf("数组没有结束")
		}
		if p.s[p.i] == ']' {
			p.i++
			n.end = p.i
			return n, nil
		}
		v, err := p.value()
		if err != nil {
			return nil, err
		}
		n.elems = append(n.elems, v)
		if err := p.skip(); err != nil {
			return nil, err
		}
		if p.i < len(p.s) && p.s[p.i] == ',' {
			p.i++
		}
	}
}

func parseJSONDoc(src string) (*jsonDoc, error) {
	p := &jscanner{s: src}
	root, err := p.value()
	if err != nil {
		return nil, err
	}
	if err := p.skip(); err != nil {
		return nil, err
	}
	if p.i != len(src) {
		return nil, p.errf("根节点之后还有多余内容")
	}
	d := &jsonDoc{src: src, root: root, eol: "\n", unit: "  "}
	if strings.Contains(src, "\r\n") {
		d.eol = "\r\n"
	}
	d.unit = detectIndent(src)
	return d, nil
}

// detectIndent 取第一处以空白开头的行的缩进作为一级缩进。
func detectIndent(src string) string {
	for _, line := range strings.Split(src, "\n") {
		line = strings.TrimRight(line, "\r")
		trim := strings.TrimLeft(line, " \t")
		if trim == "" || trim == line {
			continue
		}
		return line[:len(line)-len(trim)]
	}
	return "  "
}

// standardize 把注释与尾逗号替换成空格，得到等长的标准 JSON，用于读取节点值。
func standardize(src string) string {
	b := []byte(src)
	i := 0
	for i < len(b) {
		switch {
		case b[i] == '"':
			i++
			for i < len(b) && b[i] != '"' {
				if b[i] == '\\' {
					i++
				}
				i++
			}
			i++
		case b[i] == '/' && i+1 < len(b) && b[i+1] == '/':
			for i < len(b) && b[i] != '\n' {
				b[i] = ' '
				i++
			}
		case b[i] == '/' && i+1 < len(b) && b[i+1] == '*':
			end := bytes.Index(b[i+2:], []byte("*/"))
			stop := len(b)
			if end >= 0 {
				stop = i + 2 + end + 2
			}
			for ; i < stop; i++ {
				if b[i] != '\n' && b[i] != '\r' {
					b[i] = ' '
				}
			}
		case b[i] == ',':
			j := i + 1
			for j < len(b) {
				if b[j] == ' ' || b[j] == '\t' || b[j] == '\r' || b[j] == '\n' {
					j++
					continue
				}
				if b[j] == '/' && j+1 < len(b) && b[j+1] == '/' {
					for j < len(b) && b[j] != '\n' {
						j++
					}
					continue
				}
				if b[j] == '/' && j+1 < len(b) && b[j+1] == '*' {
					end := bytes.Index(b[j+2:], []byte("*/"))
					if end < 0 {
						j = len(b)
					} else {
						j += 2 + end + 2
					}
					continue
				}
				break
			}
			if j < len(b) && (b[j] == '}' || b[j] == ']') {
				b[i] = ' '
			}
			i++
		default:
			i++
		}
	}
	return string(b)
}

func (d *jsonDoc) member(n *jsonNode, key string) *jsonMember {
	if n == nil || n.kind != '{' {
		return nil
	}
	for i := range n.members {
		if n.members[i].key == key {
			return &n.members[i]
		}
	}
	return nil
}

func (d *jsonDoc) lookup(path []string) *jsonNode {
	cur := d.root
	for _, k := range path {
		m := d.member(cur, k)
		if m == nil {
			return nil
		}
		cur = m.val
	}
	return cur
}

func (d *jsonDoc) multiline(n *jsonNode) bool {
	return strings.Contains(d.src[n.start:n.end], "\n")
}

func (d *jsonDoc) ind(depth int) string { return strings.Repeat(d.unit, depth) }

func (d *jsonDoc) marshal(v any, depth int, compact bool) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if !compact {
		enc.SetIndent(d.ind(depth), d.unit)
	}
	if err := enc.Encode(v); err != nil {
		return "", err
	}
	s := strings.TrimRight(buf.String(), "\n")
	if d.eol != "\n" {
		s = strings.ReplaceAll(s, "\n", d.eol)
	}
	return s, nil
}

// lineIndent 返回 pos 所在行开头的空白；若 pos 之前还有别的字符，ok=false。
func lineIndent(src string, pos int) (string, bool) {
	ls := pos
	for ls > 0 && src[ls-1] != '\n' {
		ls--
	}
	seg := src[ls:pos]
	if strings.TrimLeft(seg, " \t") != "" {
		return "", false
	}
	return seg, true
}

// afterSkip 跳过 pos 之后的空白与注释，返回下一个有效字符的位置。
func afterSkip(src string, pos int) int {
	p := &jscanner{s: src, i: pos}
	_ = p.skip()
	return p.i
}

// restOfLineIsTrivial 判断 pos 到行尾是否只有空白或一条注释；是则返回行尾位置（换行符之前）。
func restOfLineIsTrivial(src string, pos int) (int, bool) {
	le := pos
	for le < len(src) && src[le] != '\n' {
		le++
	}
	end := le
	if end > pos && src[end-1] == '\r' {
		end--
	}
	rest := strings.TrimSpace(src[pos:end])
	if rest == "" || strings.HasPrefix(rest, "//") || (strings.HasPrefix(rest, "/*") && strings.HasSuffix(rest, "*/") && strings.Count(rest, "/*") == 1) {
		return end, true
	}
	return pos, false
}

type splice struct {
	at, del int
	text    string
}

func applySplices(src string, ss ...splice) string {
	// 从后往前应用，保证前面的偏移不变。
	for i := 0; i < len(ss); i++ {
		for j := i + 1; j < len(ss); j++ {
			if ss[j].at > ss[i].at {
				ss[i], ss[j] = ss[j], ss[i]
			}
		}
	}
	for _, s := range ss {
		src = src[:s.at] + s.text + src[s.at+s.del:]
	}
	return src
}

// itemSpans 返回容器内各项的 [start, end)（对象项含键）。
func itemSpans(n *jsonNode) [][2]int {
	if n.kind == '{' {
		out := make([][2]int, len(n.members))
		for i, m := range n.members {
			out[i] = [2]int{m.keyStart, m.val.end}
		}
		return out
	}
	out := make([][2]int, len(n.elems))
	for i, e := range n.elems {
		out[i] = [2]int{e.start, e.end}
	}
	return out
}

// compactDoc 整份文档没有换行时（压缩过的单行 JSON），写入的值也保持紧凑。
func (d *jsonDoc) compactDoc() bool { return !strings.Contains(d.src, "\n") }

// insertItem 在对象 / 数组末尾追加一项。key 为空表示数组元素。depth 为容器的嵌套深度。
func (d *jsonDoc) insertItem(n *jsonNode, depth int, key string, v any) (string, error) {
	spans := itemSpans(n)
	closeAt := n.end - 1
	multi := d.multiline(n)
	compact := d.compactDoc() || (len(spans) > 0 && !multi)
	val, err := d.marshal(v, depth+1, compact)
	if err != nil {
		return "", err
	}
	text := val
	if key != "" {
		text = quoteKey(key) + ": " + val
	}
	return d.insertText(n, depth, spans, closeAt, multi, text), nil
}

func (d *jsonDoc) insertText(n *jsonNode, depth int, spans [][2]int, closeAt int, multi bool, text string) string {
	if len(spans) == 0 {
		inner := d.src[n.start+1 : closeAt]
		if strings.TrimSpace(inner) == "" {
			if d.compactDoc() {
				return applySplices(d.src, splice{n.start + 1, len(inner), text})
			}
			return applySplices(d.src, splice{n.start + 1, len(inner), d.eol + d.ind(depth+1) + text + d.eol + d.ind(depth)})
		}
		return applySplices(d.src, splice{n.start + 1, 0, d.eol + d.ind(depth+1) + text})
	}
	last := spans[len(spans)-1]
	j := afterSkip(d.src, last[1])
	hasComma := j < len(d.src) && d.src[j] == ','
	if !multi {
		if hasComma {
			return applySplices(d.src, splice{j + 1, 0, " " + text})
		}
		return applySplices(d.src, splice{last[1], 0, ", " + text})
	}
	memberIndent, ok := lineIndent(d.src, last[0])
	if !ok {
		memberIndent = d.ind(depth + 1)
	}
	if hasComma {
		at, _ := restOfLineIsTrivial(d.src, j+1)
		return applySplices(d.src, splice{at, 0, d.eol + memberIndent + text + ","})
	}
	at, trivial := restOfLineIsTrivial(d.src, last[1])
	if !trivial {
		at = last[1]
	}
	ss := []splice{{last[1], 0, ","}}
	if at == last[1] {
		ss = []splice{{last[1], 0, "," + d.eol + memberIndent + text}}
	} else {
		ss = append(ss, splice{at, 0, d.eol + memberIndent + text})
	}
	return applySplices(d.src, ss...)
}

// deleteItem 删除容器中第 idx 项，连同逗号与独占的整行一并去掉。
func (d *jsonDoc) deleteItem(n *jsonNode, idx int) string {
	spans := itemSpans(n)
	if len(spans) == 1 {
		// 删掉唯一一项：容器里若没有别的内容（注释等），收拢成 [] / {}。
		before := d.src[n.start+1 : spans[0][0]]
		after := d.src[spans[0][1] : n.end-1]
		if strings.TrimSpace(before) == "" && strings.Trim(after, " \t\r\n,") == "" {
			return d.src[:n.start+1] + d.src[n.end-1:]
		}
	}
	start, end := spans[idx][0], spans[idx][1]
	j := afterSkip(d.src, end)
	followComma := j < len(d.src) && d.src[j] == ','
	if followComma {
		end = j + 1
	}
	prevComma := -1
	if !followComma && idx > 0 {
		if c := afterSkip(d.src, spans[idx-1][1]); c < len(d.src) && d.src[c] == ',' {
			prevComma = c
		}
	}
	ls := start
	for ls > 0 && (d.src[ls-1] == ' ' || d.src[ls-1] == '\t') {
		ls--
	}
	atLineStart := ls == 0 || d.src[ls-1] == '\n'
	le := end
	for le < len(d.src) && (d.src[le] == ' ' || d.src[le] == '\t') {
		le++
	}
	if rest, ok := restOfLineIsTrivial(d.src, le); ok && atLineStart {
		// 这一项独占整行：连同行尾换行一起删，前一项行尾多出来的逗号单独去掉。
		le = rest
		if le < len(d.src) && d.src[le] == '\r' {
			le++
		}
		if le < len(d.src) && d.src[le] == '\n' {
			le++
		}
		ss := []splice{{ls, le - ls, ""}}
		if prevComma >= 0 {
			ss = append(ss, splice{prevComma, 1, ""})
		}
		return applySplices(d.src, ss...)
	}
	if prevComma >= 0 {
		// 行内最后一项：把前一项之后的逗号、空白和这一项一起删掉。
		pe := spans[idx-1][1]
		return applySplices(d.src, splice{pe, end - pe, ""})
	}
	tail := end
	if tail < len(d.src) && d.src[tail] == ' ' {
		tail++
	}
	return applySplices(d.src, splice{start, tail - start, ""})
}

func quoteKey(k string) string {
	b, _ := json.Marshal(k)
	return string(b)
}

// jsonEditor 对一份 JSON(C) 文本做累积编辑。
type jsonEditor struct {
	text  string
	bom   bool
	fresh bool // 原文件为空，新建
}

const utf8BOM = "\ufeff"

func newJSONEditor(b []byte) (*jsonEditor, error) {
	s := string(b)
	e := &jsonEditor{}
	if strings.HasPrefix(s, utf8BOM) {
		e.bom = true
		s = s[len(utf8BOM):]
	}
	if strings.TrimSpace(s) == "" {
		e.fresh = true
		e.text = "{\n}"
		return e, nil
	}
	if _, err := parseJSONDoc(s); err != nil {
		return nil, err
	}
	e.text = s
	return e, nil
}

func (e *jsonEditor) doc() *jsonDoc {
	d, err := parseJSONDoc(e.text)
	if err != nil {
		// 每次编辑后都会重新校验，走到这里说明编辑器自身出错，宁可 panic 也不写坏文件。
		panic("agentcfg: jsonedit 产生了无法解析的文本：" + err.Error())
	}
	return d
}

func (e *jsonEditor) Bytes() []byte {
	s := e.text
	if e.fresh {
		d := e.doc()
		if d.eol != "\n" {
			s = strings.ReplaceAll(s, "\n", d.eol)
		}
		s += "\n"
	}
	if e.bom {
		s = utf8BOM + s
	}
	return []byte(s)
}

func (e *jsonEditor) commit(next string) error {
	if _, err := parseJSONDoc(next); err != nil {
		return fmt.Errorf("编辑后的内容无效，已放弃：%w", err)
	}
	e.text = next
	return nil
}

// Kind 返回路径处值的类型：'{' '[' 's' 'v'，不存在返回 0。
func (e *jsonEditor) Kind(path ...string) byte {
	if n := e.doc().lookup(path); n != nil {
		return n.kind
	}
	return 0
}

// Get 把路径处的值读入 out；不存在返回 false。
func (e *jsonEditor) Get(out any, path ...string) (bool, error) {
	d := e.doc()
	n := d.lookup(path)
	if n == nil {
		return false, nil
	}
	return true, json.Unmarshal([]byte(standardize(e.text)[n.start:n.end]), out)
}

// Keys 返回对象路径处的键（按出现顺序）。
func (e *jsonEditor) Keys(path ...string) []string {
	n := e.doc().lookup(path)
	if n == nil || n.kind != '{' {
		return nil
	}
	out := make([]string, len(n.members))
	for i, m := range n.members {
		out[i] = m.key
	}
	return out
}

// Len 返回数组路径处的元素个数。
func (e *jsonEditor) Len(path ...string) int {
	if n := e.doc().lookup(path); n != nil && n.kind == '[' {
		return len(n.elems)
	}
	return 0
}

// Elem 读取数组第 i 项。
func (e *jsonEditor) Elem(out any, i int, path ...string) error {
	n := e.doc().lookup(path)
	if n == nil || n.kind != '[' || i < 0 || i >= len(n.elems) {
		return errors.New("数组元素不存在")
	}
	el := n.elems[i]
	return json.Unmarshal([]byte(standardize(e.text)[el.start:el.end]), out)
}

func nestValue(path []string, v any) any {
	for i := len(path) - 1; i >= 0; i-- {
		v = Obj{{path[i], v}}
	}
	return v
}

// Set 设置路径处的值；不存在的中间对象会被创建。
func (e *jsonEditor) Set(v any, path ...string) error {
	if len(path) == 0 {
		return errors.New("路径不能为空")
	}
	d := e.doc()
	if d.root.kind != '{' {
		return errors.New("根节点不是对象，无法按键设置")
	}
	cur := d.root
	for i, key := range path {
		m := d.member(cur, key)
		last := i == len(path)-1
		if m == nil {
			next, err := d.insertItem(cur, i, key, nestValue(path[i+1:], v))
			if err != nil {
				return err
			}
			return e.commit(next)
		}
		if last {
			val, err := d.marshal(v, i+1, d.compactDoc())
			if err != nil {
				return err
			}
			return e.commit(applySplices(d.src, splice{m.val.start, m.val.end - m.val.start, val}))
		}
		if m.val.kind != '{' {
			return fmt.Errorf("%s 不是对象，无法在其下写入", strings.Join(path[:i+1], "."))
		}
		cur = m.val
	}
	return nil
}

// Delete 删除路径处的键；不存在时什么也不做。
func (e *jsonEditor) Delete(path ...string) error {
	if len(path) == 0 {
		return errors.New("路径不能为空")
	}
	d := e.doc()
	parent := d.lookup(path[:len(path)-1])
	if parent == nil || parent.kind != '{' {
		return nil
	}
	for i := range parent.members {
		if parent.members[i].key == path[len(path)-1] {
			return e.commit(d.deleteItem(parent, i))
		}
	}
	return nil
}

// ArrayAppend 在数组末尾追加一项。
func (e *jsonEditor) ArrayAppend(v any, path ...string) error {
	d := e.doc()
	n := d.lookup(path)
	if n == nil || n.kind != '[' {
		return errors.New("目标不是数组")
	}
	next, err := d.insertItem(n, len(path), "", v)
	if err != nil {
		return err
	}
	return e.commit(next)
}

// ArraySet 替换数组第 i 项。
func (e *jsonEditor) ArraySet(v any, i int, path ...string) error {
	d := e.doc()
	n := d.lookup(path)
	if n == nil || n.kind != '[' || i < 0 || i >= len(n.elems) {
		return errors.New("数组元素不存在")
	}
	val, err := d.marshal(v, len(path)+1, d.compactDoc() || !d.multiline(n))
	if err != nil {
		return err
	}
	el := n.elems[i]
	return e.commit(applySplices(d.src, splice{el.start, el.end - el.start, val}))
}

// ArrayDelete 删除数组第 i 项。
func (e *jsonEditor) ArrayDelete(i int, path ...string) error {
	d := e.doc()
	n := d.lookup(path)
	if n == nil || n.kind != '[' || i < 0 || i >= len(n.elems) {
		return errors.New("数组元素不存在")
	}
	return e.commit(d.deleteItem(n, i))
}

// Obj 保持键顺序的 JSON 对象，用于生成可读的配置片段。
type Obj []KV

// KV 是 Obj 的一项。
type KV struct {
	K string
	V any
}

// MarshalJSON 按插入顺序输出。
func (o Obj) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, kv := range o {
		if i > 0 {
			buf.WriteByte(',')
		}
		k, _ := json.Marshal(kv.K)
		buf.Write(k)
		buf.WriteByte(':')
		var vb bytes.Buffer
		enc := json.NewEncoder(&vb)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(kv.V); err != nil {
			return nil, err
		}
		buf.Write(bytes.TrimRight(vb.Bytes(), "\n"))
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}
