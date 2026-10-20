package agentcfg

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"

	"gopkg.in/yaml.v3"
)

// YAML 用 yaml.v3 的节点树编辑：注释、键顺序、引号风格都保留在节点里。
// 已知的损耗：空行不保留（写前有备份）。多文档文件、根节点类型不符的文件一律拒绝改动。

type yamlFile struct {
	doc  *yaml.Node
	root *yaml.Node
	bom  bool
	eol  string
}

func newYAMLKind(k yaml.Kind) *yaml.Node {
	if k == yaml.SequenceNode {
		return &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	}
	return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
}

// loadYAML 解析；want 是期望的根类型（映射或序列）。空文件或只有注释时得到一个空根。
func loadYAML(src []byte, want yaml.Kind) (*yamlFile, error) {
	s := string(src)
	f := &yamlFile{eol: "\n"}
	if strings.HasPrefix(s, utf8BOM) {
		f.bom = true
		s = strings.TrimPrefix(s, utf8BOM)
	}
	if strings.Contains(s, "\r\n") {
		f.eol = "\r\n"
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")

	dec := yaml.NewDecoder(strings.NewReader(s))
	var doc yaml.Node
	err := dec.Decode(&doc)
	switch {
	case errors.Is(err, io.EOF):
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{newYAMLKind(want)}}
		if t := strings.TrimSpace(s); t != "" {
			doc.HeadComment = t
		}
	case err != nil:
		return nil, fmt.Errorf("YAML 解析失败：%w", err)
	default:
		var extra yaml.Node
		if e := dec.Decode(&extra); !errors.Is(e, io.EOF) {
			if e != nil {
				return nil, fmt.Errorf("YAML 解析失败：%w", e)
			}
			return nil, errors.New("文件包含多个 YAML 文档，本工具不改动这种文件")
		}
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{newYAMLKind(want)}}
	}
	root := doc.Content[0]
	if root.Kind == yaml.ScalarNode && root.Tag == "!!null" {
		repl := newYAMLKind(want)
		repl.HeadComment, repl.LineComment, repl.FootComment = root.HeadComment, root.LineComment, root.FootComment
		doc.Content[0] = repl
		root = repl
	}
	if root.Kind != want {
		return nil, errors.New("根节点的类型不符合预期")
	}
	f.doc, f.root = &doc, root
	return f, nil
}

func (f *yamlFile) bytes() ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(f.doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	out := buf.String()
	if f.eol != "\n" {
		out = strings.ReplaceAll(out, "\n", f.eol)
	}
	if f.bom {
		out = utf8BOM + out
	}
	return []byte(out), nil
}

// MarshalYAML 让 Obj 按插入顺序输出为 YAML 映射。
func (o Obj) MarshalYAML() (any, error) {
	n := newYAMLKind(yaml.MappingNode)
	for _, kv := range o {
		k := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: kv.K}
		var v yaml.Node
		if err := v.Encode(kv.V); err != nil {
			return nil, err
		}
		n.Content = append(n.Content, k, &v)
	}
	return n, nil
}

func yamlNode(v any) (*yaml.Node, error) {
	var n yaml.Node
	if err := n.Encode(v); err != nil {
		return nil, err
	}
	return &n, nil
}

// yamlText 把值渲染成 YAML 文本，用于预览片段。
func yamlText(v any) string {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	_ = enc.Encode(v)
	_ = enc.Close()
	return strings.TrimRight(buf.String(), "\n")
}

// ymapGet 返回映射中键对应的值节点及键在 Content 中的下标。
func ymapGet(m *yaml.Node, key string) (*yaml.Node, int) {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil, -1
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1], i
		}
	}
	return nil, -1
}

// ymapSet 设置键；已存在则替换值节点（保留键上的注释）。
func ymapSet(m *yaml.Node, key string, v *yaml.Node) {
	m.Style &^= yaml.FlowStyle
	if _, i := ymapGet(m, key); i >= 0 {
		m.Content[i+1] = v
		return
	}
	k := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	m.Content = append(m.Content, k, v)
}

func ymapDel(m *yaml.Node, key string) bool {
	if _, i := ymapGet(m, key); i >= 0 {
		m.Content = append(m.Content[:i], m.Content[i+2:]...)
		return true
	}
	return false
}

// yensureMap 取得 m[key] 的映射节点；不存在则创建；类型不符报错。
func yensureMap(m *yaml.Node, key string) (*yaml.Node, error) {
	v, _ := ymapGet(m, key)
	if v == nil {
		v = newYAMLKind(yaml.MappingNode)
		ymapSet(m, key, v)
		return v, nil
	}
	if v.Kind == yaml.ScalarNode && v.Tag == "!!null" {
		repl := newYAMLKind(yaml.MappingNode)
		ymapSet(m, key, repl)
		return repl, nil
	}
	if v.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s 不是映射（可能使用了锚点引用或其他写法），本工具不改动", key)
	}
	v.Style &^= yaml.FlowStyle
	return v, nil
}

func yscalar(n *yaml.Node) string {
	if n != nil && n.Kind == yaml.ScalarNode {
		return n.Value
	}
	return ""
}

// ystrSeq 读取字符串序列。
func ystrSeq(n *yaml.Node) []string {
	if n == nil || n.Kind != yaml.SequenceNode {
		return nil
	}
	var out []string
	for _, c := range n.Content {
		out = append(out, c.Value)
	}
	return out
}
