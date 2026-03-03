package xiaohongshu

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
	"github.com/sirupsen/logrus"
)

const (
	notificationURL = "https://www.xiaohongshu.com/notification"
)

// validCommentID 校验 commentID 格式，防止 CSS 选择器注入
var validCommentID = regexp.MustCompile(`^(idx-\d+|[a-zA-Z0-9_-]+)$`)

// noteIDPattern 从链接 href 中提取笔记 ID
var noteIDPattern = regexp.MustCompile(`(?:explore|item)/([a-f0-9]+)`)

// NotificationAction 通知页操作
type NotificationAction struct {
	page *rod.Page
}

// NewNotificationAction 创建通知页操作
func NewNotificationAction(page *rod.Page) *NotificationAction {
	pp := page.Timeout(60 * time.Second)
	return &NotificationAction{page: pp}
}

// navigateToNotifications 导航到通知页并等待稳定
func navigateToNotifications(page *rod.Page) error {
	if err := page.Navigate(notificationURL); err != nil {
		return fmt.Errorf("导航到通知页失败: %w", err)
	}
	if err := page.WaitDOMStable(time.Second, 0.1); err != nil {
		logrus.Warnf("等待通知页 DOM 稳定超时: %v", err)
	}
	time.Sleep(2 * time.Second)
	return nil
}

// GetCommentNotifications 获取评论通知列表
func (n *NotificationAction) GetCommentNotifications(ctx context.Context, limit int) ([]CommentNotification, error) {
	page := n.page.Context(ctx)

	logrus.Info("导航到通知页...")
	if err := navigateToNotifications(page); err != nil {
		return nil, err
	}

	if err := clickCommentTab(page); err != nil {
		logrus.Warnf("点击评论 tab 失败: %v", err)
	}
	time.Sleep(1 * time.Second)

	notifications, err := extractCommentNotifications(page, limit)
	if err != nil {
		return nil, fmt.Errorf("提取评论通知失败: %w", err)
	}

	logrus.Infof("获取到 %d 条评论通知", len(notifications))
	return notifications, nil
}

// clickCommentTab 点击"评论和@"tab
func clickCommentTab(page *rod.Page) error {
	// 真实选择器: div.reds-tab-item.tab-item
	tabs, err := page.Elements(".reds-tab-item.tab-item")
	if err != nil {
		return fmt.Errorf("查找 tab 元素失败: %w", err)
	}

	for _, tab := range tabs {
		text, err := tab.Text()
		if err != nil {
			continue
		}
		if strings.Contains(text, "评论") {
			if err := tab.Click(proto.InputMouseButtonLeft, 1); err != nil {
				return fmt.Errorf("点击评论 tab 失败: %w", err)
			}
			logrus.Info("已点击评论 tab")
			return nil
		}
	}

	return fmt.Errorf("未找到评论 tab")
}

// extractCommentNotifications 从通知页提取评论通知（go-rod 原生 DOM 遍历）
//
// 真实 DOM 结构:
//
//	.tabs-content-container > .container  (每条通知)
//	  .user-info a                        (用户名)
//	  img.avatar-item                     (头像)
//	  .interaction-content                (评论内容)
//	  .interaction-hint span              (类型: "回复了你的评论" / "评论了你的笔记")
//	  .interaction-time                   (时间)
//	  .quote-info                         (引用原文)
//	  .extra img.extra-image              (笔记封面)
//	  .action-reply / .action-like        (操作按钮)
func extractCommentNotifications(page *rod.Page, limit int) ([]CommentNotification, error) {
	// 多种容器选择器 fallback
	selectors := []string{
		".tabs-content-container > .container",
		".notification-page .container",
		".notification-list > *",
	}

	var items rod.Elements
	for _, sel := range selectors {
		found, err := page.Elements(sel)
		if err == nil && len(found) > 0 {
			items = found
			logrus.Infof("通知列表匹配选择器: %s, 共 %d 条", sel, len(items))
			break
		}
	}

	if len(items) == 0 {
		dumpNotificationPageDOM(page)
		return nil, fmt.Errorf("未找到通知列表元素，可能小红书更新了页面结构，debug 信息已输出到日志")
	}

	var notifications []CommentNotification
	for i, item := range items {
		if limit > 0 && i >= limit {
			break
		}

		n := CommentNotification{CommentID: fmt.Sprintf("idx-%d", i)}

		// 用户名
		if userEl, err := item.Element(".user-info a"); err == nil {
			if text, err := userEl.Text(); err == nil {
				n.UserName = strings.TrimSpace(text)
			}
		}

		// 头像
		if avatarEl, err := item.Element("img.avatar-item"); err == nil {
			if src, err := avatarEl.Attribute("src"); err == nil && src != nil {
				n.UserAvatar = *src
			}
		}

		// 评论内容
		if contentEl, err := item.Element(".interaction-content"); err == nil {
			if text, err := contentEl.Text(); err == nil {
				n.Content = strings.TrimSpace(text)
			}
		}

		// 类型判断（回复/评论）
		if hintEl, err := item.Element(".interaction-hint span"); err == nil {
			if hint, err := hintEl.Text(); err == nil {
				n.IsReply = strings.Contains(hint, "回复")
			}
		}

		// 时间
		if timeEl, err := item.Element(".interaction-time"); err == nil {
			if text, err := timeEl.Text(); err == nil {
				n.Time = strings.TrimSpace(text)
			}
		}

		// 引用原文
		if quoteEl, err := item.Element(".quote-info"); err == nil {
			if text, err := quoteEl.Text(); err == nil {
				n.NoteTitle = strings.TrimSpace(text)
			}
		}

		// 笔记 ID: 从链接 href 提取
		if linkEl, err := item.Element(`a[href*="/explore/"]`); err == nil {
			if href, err := linkEl.Attribute("href"); err == nil && href != nil {
				if match := noteIDPattern.FindStringSubmatch(*href); len(match) > 1 {
					n.NoteID = match[1]
				}
			}
		}

		if n.UserName != "" || n.Content != "" {
			notifications = append(notifications, n)
		}
	}

	return notifications, nil
}

// ReplyNotificationComment 在通知页回复指定评论
func (n *NotificationAction) ReplyNotificationComment(ctx context.Context, commentID, content string) error {
	page := n.page.Context(ctx)

	if err := navigateToNotifications(page); err != nil {
		return err
	}

	_ = clickCommentTab(page)
	time.Sleep(1 * time.Second)

	commentEl, err := findNotificationComment(page, commentID)
	if err != nil {
		return fmt.Errorf("未找到评论 %s: %w", commentID, err)
	}

	commentEl.MustScrollIntoView()
	time.Sleep(500 * time.Millisecond)

	// 点击 .action-reply 按钮展开回复输入框
	replyBtn, err := commentEl.Element(".action-reply")
	if err != nil {
		return fmt.Errorf("未找到回复按钮: %w", err)
	}
	if err := replyBtn.Click(proto.InputMouseButtonLeft, 1); err != nil {
		return fmt.Errorf("点击回复按钮失败: %w", err)
	}
	time.Sleep(1 * time.Second)

	// 在展开的评论区找 textarea.comment-input
	inputEl, err := commentEl.Element("textarea.comment-input")
	if err != nil {
		return fmt.Errorf("未找到回复输入框: %w", err)
	}

	if err := inputEl.Input(content); err != nil {
		return fmt.Errorf("输入回复内容失败: %w", err)
	}
	time.Sleep(500 * time.Millisecond)

	// 点击"发送"按钮
	submitBtn, err := commentEl.Element("button.submit")
	if err != nil {
		return fmt.Errorf("未找到发送按钮: %w", err)
	}

	if err := submitBtn.Click(proto.InputMouseButtonLeft, 1); err != nil {
		return fmt.Errorf("点击发送按钮失败: %w", err)
	}

	time.Sleep(1 * time.Second)
	logrus.Infof("回复通知评论成功: commentID=%s", commentID)
	return nil
}

// LikeNotificationComment 在通知页点赞指定评论
func (n *NotificationAction) LikeNotificationComment(ctx context.Context, commentID string) error {
	page := n.page.Context(ctx)

	if err := navigateToNotifications(page); err != nil {
		return err
	}

	_ = clickCommentTab(page)
	time.Sleep(1 * time.Second)

	commentEl, err := findNotificationComment(page, commentID)
	if err != nil {
		return fmt.Errorf("未找到评论 %s: %w", commentID, err)
	}

	commentEl.MustScrollIntoView()
	time.Sleep(500 * time.Millisecond)

	// 点击 .action-like 按钮
	likeBtn, err := commentEl.Element(".action-like")
	if err != nil {
		return fmt.Errorf("未找到点赞按钮: %w", err)
	}

	if err := likeBtn.Click(proto.InputMouseButtonLeft, 1); err != nil {
		return fmt.Errorf("点击点赞按钮失败: %w", err)
	}

	time.Sleep(1 * time.Second)
	logrus.Infof("点赞通知评论成功: commentID=%s", commentID)
	return nil
}

// findNotificationComment 在通知页查找指定评论元素
func findNotificationComment(page *rod.Page, commentID string) (*rod.Element, error) {
	if !validCommentID.MatchString(commentID) {
		return nil, fmt.Errorf("无效的评论ID格式: %s", commentID)
	}

	// idx-N 索引查找
	if strings.HasPrefix(commentID, "idx-") {
		indexStr := commentID[4:]
		var index int
		if _, err := fmt.Sscanf(indexStr, "%d", &index); err == nil {
			// 多种容器选择器 fallback
			containerSelectors := []string{
				".tabs-content-container > .container",
				".notification-page .container",
				".notification-list > *",
			}
			for _, sel := range containerSelectors {
				items, err := page.Elements(sel)
				if err == nil && index < len(items) {
					return items[index], nil
				}
			}
		}
		return nil, fmt.Errorf("未找到评论: %s", commentID)
	}

	// data-id 查找
	selector := fmt.Sprintf(`[data-id="%s"]`, commentID)
	el, err := page.Timeout(3 * time.Second).Element(selector)
	if err == nil && el != nil {
		return el, nil
	}

	return nil, fmt.Errorf("未找到评论: %s", commentID)
}

// dumpNotificationPageDOM 用 go-rod 原生方式 dump 通知页 DOM 结构，用于远程调试
func dumpNotificationPageDOM(page *rod.Page) {
	// 用一小段 JS 仅做结构 dump（纯读取，不做业务逻辑）
	result, err := page.Eval(`() => {
		const root = document.querySelector('.notification-page') || document.querySelector('#app') || document.body;
		if (!root) return 'NO_ROOT';
		function walk(el, depth) {
			if (depth > 4 || !el || !el.tagName) return '';
			const tag = el.tagName.toLowerCase();
			const cls = el.className && typeof el.className === 'string'
				? '.' + el.className.trim().split(/\s+/).slice(0, 3).join('.')
				: '';
			let line = '  '.repeat(depth) + tag + cls + '[' + el.children.length + ']\n';
			for (let i = 0; i < Math.min(el.children.length, 10); i++) {
				line += walk(el.children[i], depth + 1);
			}
			return line;
		}
		return walk(root, 0);
	}`)
	if err != nil {
		logrus.Warnf("dump 通知页 DOM 失败: %v", err)
		return
	}
	logrus.Warnf("通知页 DOM 结构:\n%s", result.Value.Str())
}
