package subagent_svc

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/agentre-hub/agentre/internal/pkg/agenttool"
	"github.com/agentre-hub/agentre/internal/service/chat_svc"
)

// callAgent 解析目标 agent → 环检测 → 继承调用方项目建一次性隔离会话 → 阻塞起轮 → 回灌最终文本。
// 返回 tool result 文本或错误(错误文本回给调用 agent, 供其纠正/重试)。ref 是 subagent MCP
// token 绑定的 (agent, session) —— 即发起调用的父 agent 与其会话。
func (s *subagentSvc) callAgent(ctx context.Context, ref agenttool.Ref, agentName, prompt string) (string, error) {
	callee, err := s.agents.FindByName(ctx, agentName)
	if err != nil || callee == nil {
		return "", fmt.Errorf("未找到名为 %q 的 agent,请先用 agent_list 查看可调用 agent", agentName)
	}
	chain, reason, ok := s.resolveChain(ref.SessionID, ref.AgentID, callee.ID)
	if !ok {
		return "", errors.New(reason)
	}

	// 继承调用方会话的项目/工作目录, 让子 agent 在同一项目里干活(读不到 project 时回落 0)。
	projectID, _ := s.chat.SessionProjectID(ctx, ref.SessionID)
	resp, err := s.chat.EnsureSession(ctx, &chat_svc.EnsureSessionRequest{
		Purpose:   chat_svc.SessionPurposeSubagentCall,
		AgentID:   callee.ID,
		ProjectID: projectID,
		Title:     "子任务: " + agentName,
	})
	if err != nil || resp == nil || resp.SessionID <= 0 {
		return "", errors.New("创建子 agent 会话失败")
	}
	sessionID := resp.SessionID
	s.registerChain(sessionID, chain)
	defer s.clearChain(sessionID)

	// 订阅必须在 Send 之前(快 turn 的回执会丢)。
	turnCh, observe := s.chat.ObserveTurn(sessionID)
	defer observe()
	if _, err := s.chat.Send(ctx, &chat_svc.SendRequest{
		SessionID:             sessionID,
		AgentID:               callee.ID,
		Text:                  prompt,
		EmitTurnStartedBypass: true,
	}); err != nil {
		return "", errors.New("子 agent 起轮失败")
	}

	// 不设应用层超时:让子 agent 跑到完成。调用方 CLI 的 MCP 调用自身有上限,届时 ctx 取消,
	// 我们据此中止子 agent(下面的 ctx.Done 分支)。
	select {
	case res := <-turnCh:
		if res.Err != nil {
			return "", fmt.Errorf("子 agent 执行出错: %v", res.Err)
		}
		if res.Aborted {
			return "", errors.New("子 agent 被中止")
		}
		text, terr := s.chat.FinalAssistantText(ctx, res.AssistantMessageID)
		if terr != nil {
			return "", errors.New("读取子 agent 输出失败")
		}
		if strings.TrimSpace(text) == "" {
			return "(子 agent 完成,但没有产生文本输出)", nil
		}
		return text, nil
	case <-ctx.Done():
		// 调用方 CLI 放弃(MCP 调用超时)或用户取消 → 中止子 agent, 不留悬空 turn。
		_, _ = s.chat.Stop(context.Background(), &chat_svc.StopRequest{SessionID: sessionID})
		return "", errors.New("子 agent 调用被取消")
	}
}
