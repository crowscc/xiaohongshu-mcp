package xiaohongshu

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
	"github.com/sirupsen/logrus"
)

const (
	notificationURL = "https://www.xiaohongshu.com/notification"
)

// NotificationAction 通知页操作
type NotificationAction struct {
	page *rod.Page
}

// NewNotificationAction 创建通知页操作
func NewNotificationAction(page *rod.Page) *NotificationAction {
	pp := page.Timeout(60 * time.Second)
	return &NotificationAction{page: pp}
}

// GetCommentNotifications 获取评论通知列表
func (n *NotificationAction) GetCommentNotifications(ctx context.Context, limit int) ([]CommentNotification, error) {
	page := n.page.Context(ctx)

	logrus.Info("导航到通知页...")
	page.MustNavigate(notificationURL)
	page.MustWaitDOMStable()
	time.Sleep(2 * time.Second)

	if err := clickCommentTab(page); err != nil {
		logrus.Warnf("点击评论 tab 失败（可能页面结构不同）: %v", err)
	}
	time.Sleep(1 * time.Second)

	notifications, err := extractCommentNotifications(page, limit)
	if err != nil {
		return nil, fmt.Errorf("提取评论通知失败: %w", err)
	}

	logrus.Infof("获取到 %d 条评论通知", len(notifications))
	return notifications, nil
}

// clickCommentTab 点击评论和@tab
func clickCommentTab(page *rod.Page) error {
	tabs, err := page.Elements(".tab-item, .menu-item, [class*='tab']")
	if err != nil {
		return fmt.Errorf("查找 tab 元素失败: %w", err)
	}

	for _, tab := range tabs {
		text, err := tab.Text()
		if err != nil {
			continue
		}
		if len(text) > 0 && containsSubstr(text, "评论") {
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
func extractCommentNotifications(page *rod.Page, limit int) ([]CommentNotification, error) {
	result, err := page.Eval(fmt.Sprintf(`() => {
		const notifications = [];
		const items = document.querySelectorAll(
			'.notification-item, .comment-item, .message-item, [class*="notify-item"], [class*="comment-notify"]'
		);

		const limit = %d;

		for (let i = 0; i < items.length && (limit <= 0 || i < limit); i++) {
			const item = items[i];

			const userEl = item.querySelector('.user-name, .name, [class*="nickname"], [class*="user-info"] .name');
			const userName = userEl ? userEl.textContent.trim() : '';

			const avatarEl = item.querySelector('img.avatar, .avatar img, [class*="avatar"] img');
			const userAvatar = avatarEl ? avatarEl.src : '';

			const contentEl = item.querySelector('.content, .comment-content, [class*="content"]');
			const content = contentEl ? contentEl.textContent.trim() : '';

			const noteEl = item.querySelector('.note-title, .title, [class*="note"] .title');
			const noteTitle = noteEl ? noteEl.textContent.trim() : '';

			const linkEl = item.querySelector('a[href*="/explore/"], a[href*="/discovery/item/"]');
			let noteId = '';
			if (linkEl) {
				const href = linkEl.getAttribute('href');
				const match = href.match(/(?:explore|item)\/([a-f0-9]+)/);
				if (match) noteId = match[1];
			}

			const timeEl = item.querySelector('.time, .date, [class*="time"]');
			const time = timeEl ? timeEl.textContent.trim() : '';

			const commentId = item.getAttribute('data-id') || item.getAttribute('id') || ('idx-' + i);

			if (userName || content) {
				notifications.push({
					commentId: commentId,
					userName: userName,
					userAvatar: userAvatar,
					content: content,
					noteTitle: noteTitle,
					noteId: noteId,
					time: time,
					isReply: content.includes('回复') || false,
				});
			}
		}

		return JSON.stringify(notifications);
	}`, limit))

	if err != nil {
		return nil, fmt.Errorf("执行 JS 提取失败: %w", err)
	}

	jsonStr := result.Value.Str()
	if jsonStr == "" || jsonStr == "[]" {
		logrus.Warn("JS 提取未获取到数据，尝试 DOM 遍历方式")
		return extractNotificationsViaDOM(page, limit)
	}

	var notifications []CommentNotification
	if err := json.Unmarshal([]byte(jsonStr), &notifications); err != nil {
		return nil, fmt.Errorf("解析通知数据失败: %w", err)
	}

	return notifications, nil
}

// extractNotificationsViaDOM DOM 遍历方式提取通知
func extractNotificationsViaDOM(page *rod.Page, limit int) ([]CommentNotification, error) {
	elements, err := page.Elements(".notification-list > *, .message-list > *, [class*='notify'] > *")
	if err != nil || len(elements) == 0 {
		return nil, fmt.Errorf("未找到通知列表元素")
	}

	var notifications []CommentNotification
	for i, el := range elements {
		if limit > 0 && i >= limit {
			break
		}

		text, err := el.Text()
		if err != nil || text == "" {
			continue
		}

		notification := CommentNotification{
			CommentID: fmt.Sprintf("idx-%d", i),
			Content:   text,
		}
		notifications = append(notifications, notification)
	}

	return notifications, nil
}

// containsSubstr 检查字符串是否包含子串
func containsSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// ReplyNotificationComment 在通知页回复指定评论
func (n *NotificationAction) ReplyNotificationComment(ctx context.Context, commentID, content string) error {
	page := n.page.Context(ctx)

	page.MustNavigate(notificationURL)
	page.MustWaitDOMStable()
	time.Sleep(2 * time.Second)

	_ = clickCommentTab(page)
	time.Sleep(1 * time.Second)

	commentEl, err := findNotificationComment(page, commentID)
	if err != nil {
		return fmt.Errorf("未找到评论 %s: %w", commentID, err)
	}

	commentEl.MustScrollIntoView()
	time.Sleep(500 * time.Millisecond)

	replyBtn, err := commentEl.Element(".reply, [class*='reply'], button[class*='reply']")
	if err != nil {
		if err := commentEl.Click(proto.InputMouseButtonLeft, 1); err != nil {
			return fmt.Errorf("无法激活评论回复: %w", err)
		}
		time.Sleep(500 * time.Millisecond)

		replyBtn, err = page.Element(".reply-input, [class*='reply'] input, [class*='reply'] textarea")
		if err != nil {
			return fmt.Errorf("无法找到回复输入框: %w", err)
		}
	} else {
		if err := replyBtn.Click(proto.InputMouseButtonLeft, 1); err != nil {
			return fmt.Errorf("点击回复按钮失败: %w", err)
		}
		time.Sleep(500 * time.Millisecond)
	}

	inputEl, err := page.Element("input[class*='reply'], textarea[class*='reply'], .reply-input input, .reply-input textarea, div.input-box div.content-edit p.content-input")
	if err != nil {
		return fmt.Errorf("未找到回复输入框: %w", err)
	}

	if err := inputEl.Input(content); err != nil {
		return fmt.Errorf("输入回复内容失败: %w", err)
	}
	time.Sleep(500 * time.Millisecond)

	submitBtn, err := page.Element("button.submit, button[class*='send'], button[class*='submit'], div.bottom button.submit")
	if err != nil {
		return fmt.Errorf("未找到提交按钮: %w", err)
	}

	if err := submitBtn.Click(proto.InputMouseButtonLeft, 1); err != nil {
		return fmt.Errorf("点击提交按钮失败: %w", err)
	}

	time.Sleep(1 * time.Second)
	logrus.Infof("回复通知评论成功: commentID=%s", commentID)
	return nil
}

// LikeNotificationComment 在通知页点赞指定评论
func (n *NotificationAction) LikeNotificationComment(ctx context.Context, commentID string) error {
	page := n.page.Context(ctx)

	page.MustNavigate(notificationURL)
	page.MustWaitDOMStable()
	time.Sleep(2 * time.Second)

	_ = clickCommentTab(page)
	time.Sleep(1 * time.Second)

	commentEl, err := findNotificationComment(page, commentID)
	if err != nil {
		return fmt.Errorf("未找到评论 %s: %w", commentID, err)
	}

	commentEl.MustScrollIntoView()
	time.Sleep(500 * time.Millisecond)

	likeBtn, err := commentEl.Element(".like, [class*='like'], .like-btn, [class*='heart']")
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
	selector := fmt.Sprintf(`[data-id="%s"]`, commentID)
	el, err := page.Timeout(3 * time.Second).Element(selector)
	if err == nil && el != nil {
		return el, nil
	}

	selector = fmt.Sprintf(`#%s`, commentID)
	el, err = page.Timeout(3 * time.Second).Element(selector)
	if err == nil && el != nil {
		return el, nil
	}

	if len(commentID) > 4 && commentID[:4] == "idx-" {
		indexStr := commentID[4:]
		var index int
		if _, err := fmt.Sscanf(indexStr, "%d", &index); err == nil {
			items, err := page.Elements(".notification-item, .comment-item, .message-item, [class*='notify-item']")
			if err == nil && index < len(items) {
				return items[index], nil
			}
		}
	}

	return nil, fmt.Errorf("未找到评论: %s", commentID)
}
