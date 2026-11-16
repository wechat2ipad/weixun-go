package weixun

import "context"

// GroupService 封装群管理接口（协议端 /group/* 共 21 个端点）。
type GroupService struct {
	c *Client
}

// SetAnnouncement 设置群公告。
func (s *GroupService) SetAnnouncement(ctx context.Context, chatroom, content string) error {
	return s.c.call(ctx, "POST", "/group/SetChatroomAnnouncement", map[string]interface{}{
		"ChatRoomName": chatroom,
		"Content":      content,
	}, nil)
}

// SetName 修改群名称。
func (s *GroupService) SetName(ctx context.Context, chatroom, name string) error {
	return s.c.call(ctx, "POST", "/group/SetChatroomName", map[string]interface{}{
		"ChatRoomName": chatroom,
		"Nickname":     name,
	}, nil)
}

// Invite 邀请成员入群。
func (s *GroupService) Invite(ctx context.Context, chatroom string, members []string) error {
	return s.c.call(ctx, "POST", "/group/InviteChatroomMembers", map[string]interface{}{
		"ChatRoomName": chatroom,
		"UserList":     members,
	}, nil)
}

// Remove 移出群成员。
func (s *GroupService) Remove(ctx context.Context, chatroom string, members []string) error {
	return s.c.call(ctx, "POST", "/group/SendDelDelChatRoomMember", map[string]interface{}{
		"ChatRoomName": chatroom,
		"UserList":     members,
	}, nil)
}

// Dismiss 解散群聊（不可恢复）。
func (s *GroupService) Dismiss(ctx context.Context, chatroom string) error {
	return s.c.call(ctx, "POST", "/group/DismissChatroom", map[string]interface{}{
		"ChatRoomName": chatroom,
	}, nil)
}

// Quit 退出群聊。
func (s *GroupService) Quit(ctx context.Context, chatroom string) error {
	return s.c.call(ctx, "POST", "/group/QuitChatroom", map[string]interface{}{
		"ChatRoomName": chatroom,
	}, nil)
}

// GetInfo 获取群详情。
func (s *GroupService) GetInfo(ctx context.Context, chatrooms []string, out interface{}) error {
	return s.c.call(ctx, "POST", "/group/GetChatRoomInfo", map[string]interface{}{
		"ChatRoomWxIdList": chatrooms,
	}, out)
}

// GetMembers 获取群成员详情。
func (s *GroupService) GetMembers(ctx context.Context, chatroom string, out interface{}) error {
	return s.c.call(ctx, "POST", "/group/GetChatroomMemberDetail", map[string]interface{}{
		"ChatRoomName": chatroom,
	}, out)
}

// GetQrCode 获取群二维码。
func (s *GroupService) GetQrCode(ctx context.Context, chatroom string, out interface{}) error {
	return s.c.call(ctx, "POST", "/group/GetChatroomQrCode", map[string]interface{}{
		"ChatRoomName": chatroom,
	}, out)
}

// ─────────────────────────────────────────────

// UserService 封装用户与账号接口（协议端 /user/* 共 26 个端点）。
type UserService struct {
	c *Client
}

// GetProfile 获取当前登录账号的个人资料。
func (s *UserService) GetProfile(ctx context.Context, out interface{}) error {
	return s.c.call(ctx, "GET", "/user/GetProfile", nil, out)
}

// GetQRCode 获取当前账号的二维码。
func (s *UserService) GetQRCode(ctx context.Context, out interface{}) error {
	return s.c.call(ctx, "GET", "/user/GetQRCode", nil, out)
}

// SetProxy 设置账号代理。
func (s *UserService) SetProxy(ctx context.Context, proxy string) error {
	return s.c.call(ctx, "POST", "/user/SetProxy", map[string]interface{}{
		"Proxy": proxy,
	}, nil)
}

// ─────────────────────────────────────────────

// FriendService 封装好友相关接口（协议端 /friend/* 共 15 个端点）。
type FriendService struct {
	c *Client
}

// GetList 获取好友列表。
func (s *FriendService) GetList(ctx context.Context, out interface{}) error {
	return s.c.call(ctx, "GET", "/friend/GetFriendList", nil, out)
}

// GroupList 获取分组列表。
func (s *FriendService) GroupList(ctx context.Context, out interface{}) error {
	return s.c.call(ctx, "GET", "/friend/GroupList", nil, out)
}

// GetDetail 获取好友详情。
func (s *FriendService) GetDetail(ctx context.Context, wxids []string, out interface{}) error {
	return s.c.call(ctx, "POST", "/friend/GetContactDetail", map[string]interface{}{
		"WxIdList": wxids,
	}, out)
}

// ─────────────────────────────────────────────

// SnsService 封装朋友圈接口（协议端 /sns/* 共 17 个端点）。
type SnsService struct {
	c *Client
}

// SendFriendCircle 发布朋友圈。
func (s *SnsService) SendFriendCircle(ctx context.Context, body map[string]interface{}) error {
	return s.c.call(ctx, "POST", "/sns/SendFriendCircle", body, nil)
}

// GetTimeline 拉取朋友圈时间线。
//
// 注意：返回里的 ID / MaxID 是 64 位，SDK 已按字符串处理；
// 续拉时把返回的 MaxID 原样传回即可。
func (s *SnsService) GetTimeline(ctx context.Context, maxID string, out interface{}) error {
	return s.c.call(ctx, "POST", "/sns/SendSnsTimeLine", map[string]interface{}{
		"MaxID": maxID, // 字符串，避免精度丢失
	}, out)
}

// Comment 评论朋友圈。
func (s *SnsService) Comment(ctx context.Context, body map[string]interface{}) error {
	return s.c.call(ctx, "POST", "/sns/SendSnsComment", body, nil)
}
