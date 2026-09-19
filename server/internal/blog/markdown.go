package blog

import (
	"bytes"
	"regexp"
	"strings"
	"unicode"

	"github.com/yuin/goldmark"
	highlighting "github.com/yuin/goldmark-highlighting/v2"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
)

// TocItem 文章目录项：锚点 id 由 goldmark 自动标题 id 生成，标题文本来自节点。
type TocItem struct {
	ID    string `json:"id"`
	Text  string `json:"text"`
	Level int    `json:"level"`
}

// RenderResult goldmark 渲染产物：content_html 随内容 API 返回给 Next 展示层。
type RenderResult struct {
	HTML string
	TOC  []TocItem
}

// newMarkdown 构建博客正文渲染器：GFM（表格/任务列表/删除线/Linkify）+ 脚注 +
// 自动标题锚点 + Chroma 服务端代码高亮（monokai，即设计稿的深夜配色）。
// 默认不透传 raw HTML（goldmark 默认转义），无存储型 XSS 面。
func newMarkdown() goldmark.Markdown {
	return goldmark.New(
		goldmark.WithExtensions(
			extension.GFM,
			extension.NewFootnote(),
			// chroma 的 html formatter 默认内联样式（WithClasses=false），
			// monokai 即「深夜排字房」的暗底代码块配色，前台无需额外 CSS。
			highlighting.NewHighlighting(highlighting.WithStyle("monokai")),
		),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
		goldmark.WithRendererOptions(html.WithXHTML()),
	)
}

// Render 把 Markdown 渲染为 HTML 并抽取 h2/h3 目录。
// Callout（> [!TIP] 等 GFM alerts 语法）在 goldmark 1.8 尚无内建扩展，
// 按计划文档的回退方案在渲染后做受限 HTML 重写，产出设计稿的 callout 结构。
func Render(source string) RenderResult {
	src := []byte(source)
	md := newMarkdown()
	doc := md.Parser().Parse(text.NewReader(src))

	toc := make([]TocItem, 0, 8)
	for n := doc.FirstChild(); n != nil; n = n.NextSibling() {
		heading, ok := n.(*ast.Heading)
		if !ok || heading.Level < 2 || heading.Level > 3 {
			continue
		}
		id, _ := heading.AttributeString("id")
		idStr, _ := id.([]byte)
		toc = append(toc, TocItem{ID: string(idStr), Text: headingText(heading, src), Level: heading.Level})
	}

	var buf bytes.Buffer
	if err := md.Renderer().Render(&buf, src, doc); err != nil {
		return RenderResult{HTML: "", TOC: toc}
	}
	return RenderResult{HTML: rewriteCallouts(buf.String()), TOC: toc}
}

// calloutOpenRe 匹配以 [!KIND] 标记开头的 blockquote（GFM alerts 语法）。
// 标记独占一段或与正文同段都能命中；callout 内再嵌 blockquote 属边缘写作，
// 不支持（就近闭合），普通引用块完全不受影响。
var calloutOpenRe = regexp.MustCompile(
	`(?s)<blockquote>\s*<p>\[!(TIP|NOTE|IMPORTANT|WARNING|CAUTION)\]\s*(.*?)</blockquote>`)

var calloutLabels = map[string]string{
	"TIP":       "TIP",
	"NOTE":      "NOTE",
	"IMPORTANT": "IMPORTANT",
	"WARNING":   "WARNING",
	"CAUTION":   "CAUTION",
}

func rewriteCallouts(html string) string {
	return calloutOpenRe.ReplaceAllStringFunc(html, func(m string) string {
		groups := calloutOpenRe.FindStringSubmatch(m)
		kind := groups[1]
		cls := "callout"
		if kind == "WARNING" || kind == "CAUTION" || kind == "IMPORTANT" {
			cls = "callout warn"
		}
		head := `<div class="` + cls + `"><p class="cl-t">` + calloutLabels[kind] + `</p>`
		if strings.TrimSpace(groups[2]) != "" {
			head += "<p>" + groups[2] + "</p>"
		}
		return head + "</div>"
	})
}

// headingText 取标题节点的纯文本。
func headingText(n ast.Node, src []byte) string {
	var sb strings.Builder
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		if t, ok := c.(*ast.Text); ok {
			sb.Write(t.Segment.Value(src))
		}
	}
	return sb.String()
}

var (
	mdCodeFenceRe  = regexp.MustCompile("(?s)```.*?```")
	mdInlineCodeRe = regexp.MustCompile("`[^`]*`")
	mdImageRe      = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)
	mdLinkRe       = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	mdMarkerRe     = regexp.MustCompile(`(^|\n)(#{1,6} |> |\* |- |\d+\. )`)
	mdEmphasisRe   = regexp.MustCompile(`[*_~]+`)
)

// StripMarkdown 剥除 Markdown 语法得到纯文本，用于字数统计与摘要截取。
// 只处理博客正文中实际会用到的语法（代码块、行内代码、图片、链接、标记行、强调）。
func StripMarkdown(source string) string {
	s := mdCodeFenceRe.ReplaceAllString(source, " ")
	s = mdInlineCodeRe.ReplaceAllString(s, " ")
	s = mdImageRe.ReplaceAllString(s, " ")
	s = mdLinkRe.ReplaceAllString(s, "$1")
	s = mdMarkerRe.ReplaceAllString(s, "$1")
	s = mdEmphasisRe.ReplaceAllString(s, "")
	return strings.Join(strings.Fields(s), " ")
}

// CountWords 统计字数：CJK 字符按字计，其余连续拉丁字母数字按词计。
func CountWords(source string) int {
	text := []rune(StripMarkdown(source))
	total := 0
	letters := 0
	flush := func() {
		if letters > 0 {
			total++
			letters = 0
		}
	}
	for _, r := range text {
		switch {
		case unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r):
			flush()
			total++
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			letters++
		default:
			flush()
		}
	}
	flush()
	return total
}

// SummaryFromContent 从正文截取摘要：无手工摘要时取纯文本前 120 字。
func SummaryFromContent(source string) string {
	text := []rune(StripMarkdown(source))
	if len(text) > 120 {
		return string(text[:120]) + "…"
	}
	return string(text)
}
