package main

import "github.com/charmbracelet/lipgloss"

// 品牌色系
var (
	ColorPrimary   = lipgloss.Color("#6C5CE7") // 紫色 - OShin 品牌
	ColorSuccess   = lipgloss.Color("#00B894") // 绿色 - 成功
	ColorError     = lipgloss.Color("#E17055") // 红色 - 错误
	ColorWarning   = lipgloss.Color("#FDCB6E") // 黄色 - 警告
	ColorMuted     = lipgloss.Color("#636E72") // 灰色 - 次要信息
	ColorHighlight = lipgloss.Color("#74B9FF") // 蓝色 - 高亮
)

// 标题样式
var (
	TitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorPrimary).
			MarginBottom(1)

	SubtitleStyle = lipgloss.NewStyle().
			Foreground(ColorMuted).
			MarginBottom(1)
)

// 状态样式
var (
	SuccessStyle = lipgloss.NewStyle().
			Foreground(ColorSuccess).
			Bold(true)

	ErrorStyle = lipgloss.NewStyle().
			Foreground(ColorError).
			Bold(true)

	WarningStyle = lipgloss.NewStyle().
			Foreground(ColorWarning).
			Bold(true)

	InfoStyle = lipgloss.NewStyle().
			Foreground(ColorHighlight)
)

// 键值对样式（用于 probe、has-resume 输出）
var (
	KeyStyle = lipgloss.NewStyle().
			Foreground(ColorMuted).
			Width(14).
			Bold(true)

	ValStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("255"))
)

// 表格/分隔线
var (
	SeparatorStyle = lipgloss.NewStyle().
			Foreground(ColorMuted).
			PaddingTop(0).
			PaddingBottom(0)

	HeaderStyle = lipgloss.NewStyle().
			Foreground(ColorPrimary).
			Bold(true).
			Underline(true)
)

// 帮助文本样式
var (
	HelpCmdStyle = lipgloss.NewStyle().
			Foreground(ColorHighlight).
			Bold(true)

	HelpDescStyle = lipgloss.NewStyle().
			Foreground(ColorMuted)
)

// 输出完成后的 "Saved:" 信息
var (
	SavedStyle = lipgloss.NewStyle().
			Foreground(ColorSuccess).
			Bold(true)
)

// 渲染键值对行
func renderKV(key, val string) string {
	return KeyStyle.Render(key) + ValStyle.Render(val)
}
