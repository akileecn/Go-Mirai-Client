package bot

import (
	"bytes"
	_ "image/gif" // 用于解决发不出图片的问题
	_ "image/jpeg"
	_ "image/png"
	"math"
	"strconv"
	_ "unsafe"

	"github.com/ProtobufBot/Go-Mirai-Client/pkg/cache"
	"github.com/ProtobufBot/Go-Mirai-Client/pkg/clz"
	"github.com/ProtobufBot/Go-Mirai-Client/pkg/config"
	"github.com/ProtobufBot/Go-Mirai-Client/proto_gen/onebot"

	"github.com/Mrs4s/MiraiGo/client"
	"github.com/Mrs4s/MiraiGo/message"
	"github.com/Mrs4s/MiraiGo/utils"
	log "github.com/sirupsen/logrus"
)

const MAX_TEXT_LENGTH = 80

// 风控临时解决方案
func splitText(content string, limit int) []string {
	text := []rune(content)

	result := make([]string, 0)
	num := int(math.Ceil(float64(len(text)) / float64(limit)))
	for i := 0; i < num; i++ {
		start := i * limit
		end := func() int {
			if (i+1)*limit > len(text) {
				return len(text)
			} else {
				return (i + 1) * limit
			}
		}()
		result = append(result, string(text[start:end]))
	}
	return result
}

// 预处理私聊消息，上传图片，MiraiGo更新后删除
func preProcessPrivateSendingMessage(cli *client.QQClient, target int64, m *message.SendingMessage) {
	newElements := make([]message.IMessageElement, 0, len(m.Elements))
	for _, element := range m.Elements {
		if _, ok := element.(*clz.PokeElement); ok {
			cli.SendFriendPoke(target)
			continue
		}
		if i, ok := element.(*clz.LocalImageElement); ok {
			img, err := cli.UploadPrivateImage(target, i.Stream)
			if err != nil {
				log.Errorf("failed to upload private image, %+v", err)
				continue
			}
			if i.Tp == "flash" {
				img.Flash = true
			}
			newElements = append(newElements, img)
			continue
		}
		if i, ok := element.(*message.VoiceElement); ok {
			gm, err := cli.UploadPrivatePtt(target, bytes.NewReader(i.Data))
			if err != nil {
				log.Errorf("failed to upload private ptt, %+v", err)
				continue
			}
			newElements = append(newElements, gm)
			continue
		}
		if i, ok := element.(*clz.MyVideoElement); ok {
			gm, err := cli.UploadGroupShortVideo(target, i.UploadingVideo, i.UploadingCover)
			if err != nil {
				log.Errorf("failed to upload private video, %+v", err)
				continue
			}
			newElements = append(newElements, gm)
			continue
		}
		if i, ok := element.(*message.MusicShareElement); ok {
			// TODO 撤回？
			cli.SendFriendMusicShare(target, i)
			continue
		}
		newElements = append(newElements, element)
	}
	m.Elements = newElements
}

// 预处理群消息，上传图片/语音，MiraiGo更新后删除
func preProcessGroupSendingMessage(cli *client.QQClient, groupCode int64, m *message.SendingMessage) {
	newElements := make([]message.IMessageElement, 0, len(m.Elements))
	for _, element := range m.Elements {
		if i, ok := element.(*message.TextElement); ok {
			for _, text := range utils.ChunkString(i.Content, MAX_TEXT_LENGTH) {
				if text != "" {
					newElements = append(newElements, message.NewText(text))
				}
			}
			continue
		}
		if i, ok := element.(*clz.LocalImageElement); ok {
			img, err := cli.UploadGroupImage(groupCode, i.Stream)
			if err != nil {
				log.Errorf("failed to upload group image, %+v", err)
				continue
			}
			if i.Tp == "flash" {
				img.Flash = true
			} else if i.Tp == "show" {
				img.EffectID = i.EffectId
			}
			newElements = append(newElements, img)
			continue
		}
		if i, ok := element.(*message.VoiceElement); ok {
			gm, err := cli.UploadGroupPtt(groupCode, bytes.NewReader(i.Data))
			if err != nil {
				log.Errorf("failed to upload group ptt, %+v", err)
				continue
			}
			newElements = append(newElements, gm)
			continue
		}
		if i, ok := element.(*message.AtElement); ok && i.Target != 0 {
			i.Display = "@" + func() string {
				mem := cli.FindGroup(groupCode).FindMember(i.Target)
				if mem != nil {
					return mem.DisplayName()
				}
				return strconv.FormatInt(i.Target, 10)
			}()
			newElements = append(newElements, i)
			continue
		}
		if i, ok := element.(*clz.PokeElement); ok {
			cli.SendGroupPoke(groupCode, i.Target)
			continue
		}
		if i, ok := element.(*clz.MyVideoElement); ok {
			gm, err := cli.UploadGroupShortVideo(groupCode, i.UploadingVideo, i.UploadingCover)
			if err != nil {
				log.Errorf("failed to upload group video, %+v", err)
				continue
			}
			newElements = append(newElements, gm)
			continue
		}
		if i, ok := element.(*message.MusicShareElement); ok {
			// TODO 撤回？
			_, err := cli.SendGroupMusicShare(groupCode, i)
			if err != nil {
				log.Errorf("failed to send music, %+v", err)
				continue
			}
			continue
		}
		newElements = append(newElements, element)
	}
	m.Elements = newElements
}

func HandleSendPrivateMsg(cli *client.QQClient, req *onebot.SendPrivateMsgReq) *onebot.SendPrivateMsgResp {
	miraiMsg := ProtoMsgToMiraiMsg(cli, req.Message, req.AutoEscape)
	sendingMessage := &message.SendingMessage{Elements: miraiMsg}
	log.Infof("Bot(%d) Private(%d) <- %s", cli.Uin, req.UserId, MiraiMsgToRawMsg(cli, miraiMsg))
	preProcessPrivateSendingMessage(cli, req.UserId, sendingMessage)
	ret := cli.SendPrivateMessage(req.UserId, sendingMessage)
	cache.PrivateMessageLru.Add(ret.Id, ret)
	return &onebot.SendPrivateMsgResp{
		MessageId: ret.Id,
	}
}

func HandleSendGroupMsg(cli *client.QQClient, req *onebot.SendGroupMsgReq) *onebot.SendGroupMsgResp {
	if cli.FindGroup(req.GroupId) == nil {
		log.Warnf("发送消息失败，群聊 %d 不存在", req.GroupId)
		return nil
	}
	miraiMsg := ProtoMsgToMiraiMsg(cli, req.Message, req.AutoEscape)
	sendingMessage := &message.SendingMessage{Elements: miraiMsg}
	log.Infof("Bot(%d) Group(%d) <- %s", cli.Uin, req.GroupId, MiraiMsgToRawMsg(cli, miraiMsg))
	preProcessGroupSendingMessage(cli, req.GroupId, sendingMessage)
	if len(sendingMessage.Elements) == 0 {
		log.Warnf("发送消息内容为空")
		return nil
	}
	ret := cli.SendGroupMessage(req.GroupId, sendingMessage, config.Fragment)
	if ret == nil || ret.Id == -1 {
		config.Fragment = !config.Fragment
		log.Warnf("发送群消息失败，可能被风控，下次发送将改变分片策略，Fragment: %+v", config.Fragment)
		return nil
	}
	cache.GroupMessageLru.Add(ret.Id, ret)
	return &onebot.SendGroupMsgResp{
		MessageId: ret.Id,
	}
}

func HandleSendMsg(cli *client.QQClient, req *onebot.SendMsgReq) *onebot.SendMsgResp {
	miraiMsg := ProtoMsgToMiraiMsg(cli, req.Message, req.AutoEscape)
	sendingMessage := &message.SendingMessage{Elements: miraiMsg}
	if req.UserId != 0 { // 私聊+临时
		preProcessPrivateSendingMessage(cli, req.UserId, sendingMessage)
	} else { // 群
		preProcessGroupSendingMessage(cli, req.GroupId, sendingMessage)
	}

	if req.GroupId != 0 && req.UserId != 0 { // 临时
		ret := cli.SendGroupTempMessage(req.GroupId, req.UserId, sendingMessage)
		cache.PrivateMessageLru.Add(ret.Id, ret)
		return &onebot.SendMsgResp{
			MessageId: ret.Id,
		}
	}

	if req.GroupId != 0 { // 群
		if cli.FindGroup(req.GroupId) == nil {
			log.Warnf("发送消息失败，群聊 %d 不存在", req.GroupId)
			return nil
		}
		preProcessGroupSendingMessage(cli, req.GroupId, sendingMessage)
		ret := cli.SendGroupMessage(req.GroupId, sendingMessage, config.Fragment)
		if ret == nil || ret.Id == -1 {
			config.Fragment = !config.Fragment
			log.Warnf("发送群消息失败，可能被风控，下次发送将改变分片策略，Fragment: %+v", config.Fragment)
			return nil
		}
		cache.GroupMessageLru.Add(ret.Id, ret)
		return &onebot.SendMsgResp{
			MessageId: ret.Id,
		}
	}

	if req.UserId != 0 { // 私聊
		preProcessPrivateSendingMessage(cli, req.UserId, sendingMessage)
		ret := cli.SendPrivateMessage(req.UserId, sendingMessage)
		cache.PrivateMessageLru.Add(ret.Id, ret)
		return &onebot.SendMsgResp{
			MessageId: ret.Id,
		}
	}
	log.Warnf("failed to send msg")
	return nil
}

func HandleDeleteMsg(cli *client.QQClient, req *onebot.DeleteMsgReq) *onebot.DeleteMsgResp {
	if eventInterface, ok := cache.PrivateMessageLru.Get(req.MessageId); ok {
		if event, ok := eventInterface.(*message.PrivateMessage); ok {
			if err := cli.RecallPrivateMessage(event.Target, int64(event.Time), event.Id, event.InternalId); err == nil {
				return &onebot.DeleteMsgResp{}
			}
		}
	}

	if eventInterface, ok := cache.GroupMessageLru.Get(req.MessageId); ok {
		if event, ok := eventInterface.(*message.GroupMessage); ok {
			if err := cli.RecallGroupMessage(event.GroupCode, event.Id, event.InternalId); err != nil {
				return &onebot.DeleteMsgResp{}
			}
		}
	}
	return nil
}

func HandleGetMsg(cli *client.QQClient, req *onebot.GetMsgReq) *onebot.GetMsgResp {
	eventInterface, isGroup := cache.GroupMessageLru.Get(req.MessageId)
	if isGroup {
		event := eventInterface.(*message.GroupMessage)
		messageType := "group"
		if event.Sender.Uin == cli.Uin {
			messageType = "self"
		}
		return &onebot.GetMsgResp{
			Time:        event.Time,
			MessageType: messageType,
			MessageId:   req.MessageId,
			RealId:      event.InternalId, // 不知道是什么？
			Message:     MiraiMsgToProtoMsg(cli, event.Elements),
			RawMessage:  MiraiMsgToRawMsg(cli, event.Elements),
			Sender: &onebot.GetMsgResp_Sender{
				UserId:   event.Sender.Uin,
				Nickname: event.Sender.Nickname,
			},
		}

	}
	eventInterface, isPrivate := cache.PrivateMessageLru.Get(req.MessageId)
	if isPrivate {
		event := eventInterface.(*message.PrivateMessage)
		messageType := "private"
		if event.Sender.Uin == cli.Uin {
			messageType = "self"
		}
		return &onebot.GetMsgResp{
			Time:        event.Time,
			MessageType: messageType,
			MessageId:   req.MessageId,
			RealId:      event.InternalId, // 不知道是什么？
			Message:     MiraiMsgToProtoMsg(cli, event.Elements),
			RawMessage:  MiraiMsgToRawMsg(cli, event.Elements),
			Sender: &onebot.GetMsgResp_Sender{
				UserId:   event.Sender.Uin,
				Nickname: event.Sender.Nickname,
			},
		}
	}
	return nil
}

func HandleSetGroupKick(cli *client.QQClient, req *onebot.SetGroupKickReq) *onebot.SetGroupKickResp {
	if group := cli.FindGroup(req.GroupId); group != nil {
		if member := group.FindMember(req.UserId); member != nil {
			if err := member.Kick("", req.RejectAddRequest); err != nil {
				return nil
			}
			return &onebot.SetGroupKickResp{}
		}
	}
	return nil
}

func HandleSetGroupBan(cli *client.QQClient, req *onebot.SetGroupBanReq) *onebot.SetGroupBanResp {
	if group := cli.FindGroup(req.GroupId); group != nil {
		if member := group.FindMember(req.UserId); member != nil {
			if err := member.Mute(uint32(req.Duration)); err != nil {
				return nil
			}
			return &onebot.SetGroupBanResp{}
		}
	}
	return nil
}

func HandleSetGroupWholeBan(cli *client.QQClient, req *onebot.SetGroupWholeBanReq) *onebot.SetGroupWholeBanResp {
	if group := cli.FindGroup(req.GroupId); group != nil {
		group.MuteAll(req.Enable)
		return &onebot.SetGroupWholeBanResp{}
	}
	return nil
}

func HandleSetGroupCard(cli *client.QQClient, req *onebot.SetGroupCardReq) *onebot.SetGroupCardResp {
	if group := cli.FindGroup(req.GroupId); group != nil {
		if member := group.FindMember(req.UserId); member != nil {
			member.EditCard(req.Card)
			return &onebot.SetGroupCardResp{}
		}
	}
	return nil
}

func HandleSetGroupName(cli *client.QQClient, req *onebot.SetGroupNameReq) *onebot.SetGroupNameResp {
	if group := cli.FindGroup(req.GroupId); group != nil {
		group.UpdateName(req.GroupName)
		return &onebot.SetGroupNameResp{}
	}
	return nil
}

func HandleSetGroupLeave(cli *client.QQClient, req *onebot.SetGroupLeaveReq) *onebot.SetGroupLeaveResp {
	if group := cli.FindGroup(req.GroupId); group != nil {
		group.Quit()
		return &onebot.SetGroupLeaveResp{}
	}
	return nil
}

func HandleSetGroupSpecialTitle(cli *client.QQClient, req *onebot.SetGroupSpecialTitleReq) *onebot.SetGroupSpecialTitleResp {
	if group := cli.FindGroup(req.GroupId); group != nil {
		if member := group.FindMember(req.UserId); member != nil {
			member.EditSpecialTitle(req.SpecialTitle)
			return &onebot.SetGroupSpecialTitleResp{}
		}
	}
	return nil
}

func HandleSetFriendAddRequest(cli *client.QQClient, req *onebot.SetFriendAddRequestReq) *onebot.SetFriendAddRequestResp {
	eventInterface, ok := cache.FriendRequestLru.Get(req.Flag)
	if !ok {
		return nil
	}
	event, ok := eventInterface.(*client.NewFriendRequest)
	if !ok {
		return nil
	}
	cli.SolveFriendRequest(event, req.Approve)
	return &onebot.SetFriendAddRequestResp{}
}

func HandleSetGroupAddRequest(cli *client.QQClient, req *onebot.SetGroupAddRequestReq) *onebot.SetGroupAddRequestResp {
	eventInterface, isGroupRequest := cache.GroupRequestLru.Get(req.Flag)
	if isGroupRequest {
		event, ok := eventInterface.(*client.UserJoinGroupRequest)
		if !ok {
			return nil
		}
		if req.Approve {
			event.Accept()
		} else {
			event.Reject(false, req.Reason)
		}

		return &onebot.SetGroupAddRequestResp{}
	}

	eventInterface, isBotInvited := cache.GroupInvitedRequestLru.Get(req.Flag)
	if isBotInvited {
		event, ok := eventInterface.(*client.GroupInvitedRequest)
		if !ok {
			return nil
		}
		cli.SolveGroupJoinRequest(event, req.Approve, false, req.Reason)
		return &onebot.SetGroupAddRequestResp{}
	}
	return nil
}

func HandleGetLoginInfo(cli *client.QQClient, req *onebot.GetLoginInfoReq) *onebot.GetLoginInfoResp {
	return &onebot.GetLoginInfoResp{
		UserId:   cli.Uin,
		Nickname: cli.Nickname,
	}
}

func HandleGetFriendList(cli *client.QQClient, req *onebot.GetFriendListReq) *onebot.GetFriendListResp {
	friendList := make([]*onebot.GetFriendListResp_Friend, 0)
	for _, friend := range cli.FriendList {
		friendList = append(friendList, &onebot.GetFriendListResp_Friend{
			UserId:   friend.Uin,
			Nickname: friend.Nickname,
			Remark:   friend.Remark,
		})
	}
	return &onebot.GetFriendListResp{
		Friend: friendList,
	}
}

func HandleGetGroupInfo(cli *client.QQClient, req *onebot.GetGroupInfoReq) *onebot.GetGroupInfoResp {
	if group := cli.FindGroup(req.GroupId); group != nil {
		return &onebot.GetGroupInfoResp{
			GroupId:        group.Code,
			GroupName:      group.Name,
			MaxMemberCount: int32(group.MaxMemberCount),
			MemberCount:    int32(group.MemberCount),
		}
	}
	return nil
}

func HandleGetGroupList(cli *client.QQClient, req *onebot.GetGroupListReq) *onebot.GetGroupListResp {
	groupList := make([]*onebot.GetGroupListResp_Group, 0)
	for _, group := range cli.GroupList {
		groupList = append(groupList, &onebot.GetGroupListResp_Group{
			GroupId:        group.Code,
			GroupName:      group.Name,
			MaxMemberCount: int32(group.MaxMemberCount),
			MemberCount:    int32(group.MemberCount),
		})
	}
	return &onebot.GetGroupListResp{
		Group: groupList,
	}
}

func HandleGetGroupMemberInfo(cli *client.QQClient, req *onebot.GetGroupMemberInfoReq) *onebot.GetGroupMemberInfoResp {
	if group := cli.FindGroup(req.GroupId); group != nil {
		if member := group.FindMember(req.UserId); member != nil {
			return &onebot.GetGroupMemberInfoResp{
				GroupId:      req.GroupId,
				UserId:       req.UserId,
				Nickname:     member.Nickname,
				Card:         member.CardName,
				JoinTime:     member.JoinTime,
				LastSentTime: member.LastSpeakTime,
				Level:        strconv.FormatInt(int64(member.Level), 10),
				Role: func() string {
					switch member.Permission {
					case client.Owner:
						return "owner"
					case client.Administrator:
						return "admin"
					default:
						return "member"
					}
				}(),
				Title:           member.SpecialTitle,
				TitleExpireTime: member.SpecialTitleExpireTime,
			}
		}
	}
	return nil
}

func HandleGetGroupMemberList(cli *client.QQClient, req *onebot.GetGroupMemberListReq) *onebot.GetGroupMemberListResp {
	if group := cli.FindGroup(req.GroupId); group != nil {
		members, err := cli.GetGroupMembers(group)
		if err != nil {
			log.Errorf("获取群成员列表失败")
			return nil
		}
		memberList := make([]*onebot.GetGroupMemberListResp_GroupMember, 0)
		for _, member := range members {
			memberList = append(memberList, &onebot.GetGroupMemberListResp_GroupMember{
				GroupId:      req.GroupId,
				UserId:       member.Uin,
				Nickname:     member.Nickname,
				Card:         member.CardName,
				JoinTime:     member.JoinTime,
				LastSentTime: member.LastSpeakTime,
				Level:        strconv.FormatInt(int64(member.Level), 10),
				Role: func() string {
					switch member.Permission {
					case client.Owner:
						return "owner"
					case client.Administrator:
						return "admin"
					default:
						return "member"
					}
				}(),
				Title:           member.SpecialTitle,
				TitleExpireTime: member.SpecialTitleExpireTime,
			})
		}
		return &onebot.GetGroupMemberListResp{
			GroupMember: memberList,
		}
	}
	return nil
}

func HandleGetStrangerInfo(cli *client.QQClient, req *onebot.GetStrangerInfoReq) *onebot.GetStrangerInfoResp {
	info, err := cli.GetSummaryInfo(req.UserId)
	if err != nil {
		log.Warnf("获取陌生人信息错误 %+v", err)
		return nil
	}
	return &onebot.GetStrangerInfoResp{
		UserId:   req.UserId,
		Nickname: info.Nickname,
		Sex: func() string {
			if info.Sex == 1 {
				return "female"
			}
			return "male"
		}(),
		Age:       int32(info.Age),
		Level:     info.Level,
		LoginDays: info.LoginDays,
	}
}

//go:linkname GetCookiesWithDomain github.com/Mrs4s/MiraiGo/client.(*QQClient).getCookiesWithDomain
func GetCookiesWithDomain(c *client.QQClient, domain string) string

func HandleGetCookies(cli *client.QQClient, req *onebot.GetCookiesReq) *onebot.GetCookiesResp {
	return &onebot.GetCookiesResp{Cookies: GetCookiesWithDomain(cli, req.Domain)}
}

//go:linkname GetCSRFToken github.com/Mrs4s/MiraiGo/client.(*QQClient).getCSRFToken
func GetCSRFToken(c *client.QQClient) int

func HandleGetCSRFToken(cli *client.QQClient, req *onebot.GetCsrfTokenReq) *onebot.GetCsrfTokenResp {
	return &onebot.GetCsrfTokenResp{
		Token: int32(GetCSRFToken(cli)),
	}
}
