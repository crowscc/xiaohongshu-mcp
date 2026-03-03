package xiaohongshu

import (
	"context"
	"encoding/json"
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

// extractCommentNotifications 从通知页提取评论通知
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
	result, err := page.Eval(fmt.Sprintf(`() => {
		// 多种容器选择器，适配不同版本的 DOM 结构
		const selectors = [
			'.tabs-content-container > .container',
			'.notification-page .container',
			'.notification-list > *',
			'[class*="notify"] .container',
			'[class*="comment-list"] > *',
		];

		let items = [];
		let matchedSelector = '';
		for (const sel of selectors) {
			items = document.querySelectorAll(sel);
			if (items.length > 0) {
				matchedSelector = sel;
				break;
			}
		}

		if (items.length === 0) return JSON.stringify({error: 'no_match', debug: dumpPageStructure()});

		const notifications = [];
		const limit = %d;

		for (let i = 0; i < items.length && (limit <= 0 || i < limit); i++) {
			const item = items[i];

			// 用户名: 多种选择器 fallback
			const userEl = item.querySelector('.user-info a') || item.querySelector('[class*="user"] a') || item.querySelector('.name a');
			const userName = userEl ? userEl.textContent.trim() : '';

			const avatarEl = item.querySelector('img.avatar-item') || item.querySelector('[class*="avatar"] img') || item.querySelector('img.avatar');
			const userAvatar = avatarEl ? avatarEl.src : '';

			const contentEl = item.querySelector('.interaction-content') || item.querySelector('[class*="content"]:not([class*="container"])');
			const content = contentEl ? contentEl.textContent.trim() : '';

			const hintEl = item.querySelector('.interaction-hint span') || item.querySelector('[class*="hint"] span');
			const hint = hintEl ? hintEl.textContent.trim() : '';
			const isReply = hint.includes('回复');

			const quoteEl = item.querySelector('.quote-info') || item.querySelector('[class*="quote"]');
			const noteTitle = quoteEl ? quoteEl.textContent.trim() : '';

			const timeEl = item.querySelector('.interaction-time') || item.querySelector('[class*="time"]');
			const time = timeEl ? timeEl.textContent.trim() : '';

			const linkEl = item.querySelector('a[href*="/explore/"], a[href*="/discovery/item/"], .extra a');
			let noteId = '';
			if (linkEl) {
				const href = linkEl.getAttribute('href') || '';
				const match = href.match(/(?:explore|item)\/([a-f0-9]+)/);
				if (match) noteId = match[1];
			}

			const commentId = 'idx-' + i;

			if (userName || content) {
				notifications.push({
					commentId: commentId,
					userName: userName,
					userAvatar: userAvatar,
					content: content,
					noteTitle: noteTitle,
					noteId: noteId,
					time: time,
					isReply: isReply,
				});
			}
		}
		return JSON.stringify({notifications: notifications, selector: matchedSelector});

		// DOM 结构 dump，选择器全部不匹配时用于远程调试
		function dumpPageStructure() {
			const page = document.querySelector('.notification-page') || document.querySelector('#app');
			if (!page) return 'NO_PAGE';
			function walk(el, depth) {
				if (depth > 3 || !el || !el.tagName) return '';
				const tag = el.tagName.toLowerCase();
				const cls = el.className && typeof el.className === 'string' ? el.className.split(' ').filter(c=>c).slice(0,3).join('.') : '';
				const children = el.children.length;
				let line = '  '.repeat(depth) + tag + (cls ? '.'+cls : '') + '[' + children + ']\n';
				for (let i = 0; i < Math.min(children, 10); i++) {
					line += walk(el.children[i], depth+1);
				}
				return line;
			}
			return walk(page, 0);
		}
	}`, limit))

	if err != nil {
		return nil, fmt.Errorf("执行 JS 提取失败: %w", err)
	}

	jsonStr := result.Value.Str()
	if jsonStr == "" {
		return nil, fmt.Errorf("未找到通知列表元素")
	}

	// 解析结果，可能是 {error, debug} 或 {notifications, selector}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(jsonStr), &raw); err != nil {
		return nil, fmt.Errorf("解析通知数据失败: %w", err)
	}

	// 选择器全部不匹配时，返回 debug 信息
	if _, hasError := raw["error"]; hasError {
		debugInfo := ""
		if d, ok := raw["debug"]; ok {
			json.Unmarshal(d, &debugInfo)
		}
		logrus.Warnf("通知页 DOM 选择器未匹配，页面结构:\n%s", debugInfo)
		return nil, fmt.Errorf("未找到通知列表元素，可能小红书更新了页面结构，debug 信息已输出到日志")
	}

	// 正常解析
	if matchedSel, ok := raw["selector"]; ok {
		var sel string
		json.Unmarshal(matchedSel, &sel)
		logrus.Infof("通知列表匹配选择器: %s", sel)
	}

	var notifications []CommentNotification
	if notifData, ok := raw["notifications"]; ok {
		if err := json.Unmarshal(notifData, &notifications); err != nil {
			return nil, fmt.Errorf("解析通知数据失败: %w", err)
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
