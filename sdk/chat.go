package sdk

import "github.com/chenjia404/meshproxy/internal/chat"

type Profile = chat.Profile
type Contact = chat.Contact
type Request = chat.Request
type Conversation = chat.Conversation
type Message = chat.Message
type Group = chat.Group
type GroupDetails = chat.GroupDetails
type GroupMember = chat.GroupMember
type GroupMessage = chat.GroupMessage

type ChatService struct {
	inner *chat.Service
}

func (c *ChatService) GetProfile() (Profile, error) {
	return c.inner.GetProfile()
}

func (c *ChatService) UpdateProfile(nickname string) (Profile, error) {
	return c.inner.UpdateProfile(nickname)
}

func (c *ChatService) ListContacts() ([]Contact, error) {
	return c.inner.ListContacts()
}

func (c *ChatService) UpdateContactNickname(peerID, nickname string) (Contact, error) {
	return c.inner.UpdateContactNickname(peerID, nickname)
}

func (c *ChatService) SetContactBlocked(peerID string, blocked bool) (Contact, error) {
	return c.inner.SetContactBlocked(peerID, blocked)
}

func (c *ChatService) ListRequests() ([]Request, error) {
	return c.inner.ListRequests()
}

func (c *ChatService) SendRequest(toPeerID, introText string) (Request, error) {
	return c.inner.SendRequest(toPeerID, introText)
}

func (c *ChatService) AcceptRequest(requestID string) (Conversation, error) {
	return c.inner.AcceptRequest(requestID)
}

func (c *ChatService) RejectRequest(requestID string) error {
	return c.inner.RejectRequest(requestID)
}

func (c *ChatService) ListConversations() ([]Conversation, error) {
	return c.inner.ListConversations()
}

func (c *ChatService) UpdateConversationRetention(conversationID string, minutes int) (Conversation, error) {
	return c.inner.UpdateConversationRetention(conversationID, minutes)
}

func (c *ChatService) ListMessages(conversationID string) ([]Message, error) {
	return c.inner.ListMessages(conversationID)
}

func (c *ChatService) SyncConversation(conversationID string) error {
	return c.inner.SyncConversation(conversationID)
}

func (c *ChatService) RevokeMessage(conversationID, msgID string) error {
	return c.inner.RevokeMessage(conversationID, msgID)
}

func (c *ChatService) SendFile(conversationID, fileName, mimeType string, data []byte) (Message, error) {
	return c.inner.SendFile(conversationID, fileName, mimeType, data)
}

func (c *ChatService) GetMessageFile(conversationID, msgID string) (Message, []byte, error) {
	return c.inner.GetMessageFile(conversationID, msgID)
}

func (c *ChatService) SendText(conversationID, text string) (Message, error) {
	return c.inner.SendText(conversationID, text)
}

func (c *ChatService) ConnectPeer(peerID string) error {
	return c.inner.ConnectPeer(peerID)
}

func (c *ChatService) PeerStatus(peerID string) (map[string]any, error) {
	return c.inner.PeerStatus(peerID)
}

func (c *ChatService) NetworkStatus() map[string]any {
	return c.inner.NetworkStatus()
}

func (c *ChatService) ListGroups() ([]Group, error) {
	return c.inner.ListGroups()
}

func (c *ChatService) GetGroupDetails(groupID string) (GroupDetails, error) {
	return c.inner.GetGroupDetails(groupID)
}

func (c *ChatService) CreateGroup(title string, members []string) (Group, error) {
	return c.inner.CreateGroup(title, members)
}

func (c *ChatService) InviteGroupMember(groupID, peerID, role, inviteText string) (Group, error) {
	return c.inner.InviteGroupMember(groupID, peerID, role, inviteText)
}

func (c *ChatService) AcceptGroupInvite(groupID string) (Group, error) {
	return c.inner.AcceptGroupInvite(groupID)
}

func (c *ChatService) LeaveGroup(groupID, reason string) (Group, error) {
	return c.inner.LeaveGroup(groupID, reason)
}

func (c *ChatService) RemoveGroupMember(groupID, peerID, reason string) (Group, error) {
	return c.inner.RemoveGroupMember(groupID, peerID, reason)
}

func (c *ChatService) UpdateGroupTitle(groupID, title string) (Group, error) {
	return c.inner.UpdateGroupTitle(groupID, title)
}

func (c *ChatService) UpdateGroupRetention(groupID string, minutes int) (Group, error) {
	return c.inner.UpdateGroupRetention(groupID, minutes)
}

func (c *ChatService) DissolveGroup(groupID, reason string) (Group, error) {
	return c.inner.DissolveGroup(groupID, reason)
}

func (c *ChatService) TransferGroupController(groupID, peerID string) (Group, error) {
	return c.inner.TransferGroupController(groupID, peerID)
}

func (c *ChatService) ListGroupMessages(groupID string) ([]GroupMessage, error) {
	return c.inner.ListGroupMessages(groupID)
}

func (c *ChatService) RevokeGroupMessage(groupID, msgID string) error {
	return c.inner.RevokeGroupMessage(groupID, msgID)
}

func (c *ChatService) SendGroupText(groupID, text string) (GroupMessage, error) {
	return c.inner.SendGroupText(groupID, text)
}

func (c *ChatService) SendGroupFile(groupID, fileName, mimeType string, data []byte) (GroupMessage, error) {
	return c.inner.SendGroupFile(groupID, fileName, mimeType, data)
}

func (c *ChatService) GetGroupMessageFile(groupID, msgID string) (GroupMessage, []byte, error) {
	return c.inner.GetGroupMessageFile(groupID, msgID)
}

func (c *ChatService) SyncGroup(groupID, fromPeerID string) error {
	return c.inner.SyncGroup(groupID, fromPeerID)
}
