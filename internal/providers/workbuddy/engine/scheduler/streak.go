// streak.go 连登管家：签到排程后自动检查连登兑换档位 → 可兑换即兑换 → 按抽奖次数抽奖。
//
// 背景（2026-09-12）：成长中心连登档位（7d/14d/28d）按连续登录天数解锁，兑换发
// credit/energy/补签卡/抽奖次数；抽奖次数只能从兑换获得。兑换按钮在 UI 上恒可点，
// 但未解锁时服务端 403「连续登录天数不足」——所以放在每日签到后跑一遍（幂等），
// 到天数那天自动完成「兑换 → 抽奖」闭环，无需人工盯。
package scheduler

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/orangeruan/poolservice/internal/providers/workbuddy/engine/auth"
	"github.com/orangeruan/poolservice/internal/providers/workbuddy/engine/logfmt"
)

// RunStreakBonusNow 对所有可用账号执行连登兑换 + 抽奖（幂等：locked/无次数自动跳过）。
// 由签到排程（runCheckin）末尾调用；也可面板手动触发。
func (s *Scheduler) RunStreakBonusNow() { s.runStreakBonus(nil, TriggerSchedule) }

// RunStreakBonusFor 手动触发，只处理 only 中的账号（nil = 全部；显式指定时禁用账号也执行）。
func (s *Scheduler) RunStreakBonusFor(only []string) { s.runStreakBonus(only, TriggerManual) }

func (s *Scheduler) runStreakBonus(only []string, trigger string) {
	rep := s.begin("streak", only, trigger)
	defer rep.End()
	for _, st := range s.listFor(only) {
		if st.Disabled && only == nil {
			continue
		}
		a := s.cfg.Pool.AuthByUID(st.UID)
		if a == nil || a.AccessTokenValue() == "" {
			rep.Item(st.UID, st.Nickname, StatusSkipped, "凭证缺少 access token", 0, 0)
			continue
		}
		if a.IsGlobal() {
			rep.Item(st.UID, st.Nickname, StatusSkipped, "国际版没有连登体系", 0, 0)
			continue // D4 门控：global 无 CN 任务体系，不发起任何上游调用
		}
		if a.IsEnterprise() {
			rep.Item(st.UID, st.Nickname, StatusSkipped, "企业版没有成长体系", 0, 0)
			continue // 企业版门控：无成长体系（/activity/growth/streak 上游一律 403）
		}
		r := s.streakBonusAccount(a)
		status := StatusOK
		switch {
		case r.err != nil:
			status = StatusFailed
		case len(r.parts) == 0:
			status = StatusSkipped
			r.parts = []string{"没有可兑换的档位或抽奖次数"}
		}
		msg := strings.Join(r.parts, "；")
		if r.err != nil {
			msg = strings.TrimPrefix(msg+"；"+r.err.Error(), "；")
		}
		rep.Item(st.UID, st.Nickname, status, msg, float64(r.credit), float64(r.energy))
	}
}

type streakResult struct {
	parts          []string
	credit, energy int64
	err            error
}

// streakBonusAccount 单账号：补签保连登 → 礼包/补偿 → 兑换所有已解锁档位 → 抽完所有 chances。
func (s *Scheduler) streakBonusAccount(a *auth.Auth) (r streakResult) {
	// 0. 补签保连登：昨日漏签且有补签卡则补上（连续天数一断就要重攒 7 天）。
	if day := s.makeupYesterday(a); day != "" {
		r.parts = append(r.parts, "补签 "+day)
	}
	// 0.5 礼包/补偿（每号一次，无则业务错误静默跳过）。
	if credit, err := s.cfg.Upstream.ClaimGift(a); err == nil {
		log.Printf("streak-bonus %s: 🎊 新手礼包 +%dc", logfmt.Label(a.UID, a.Nickname), credit)
		r.parts = append(r.parts, fmt.Sprintf("新手礼包 +%d", credit))
		r.credit += credit
	}
	if credit, err := s.cfg.Upstream.ClaimCompensation(a); err == nil {
		log.Printf("streak-bonus %s: 🎊 补偿领取 +%dc", logfmt.Label(a.UID, a.Nickname), credit)
		r.parts = append(r.parts, fmt.Sprintf("补偿 +%d", credit))
		r.credit += credit
	}

	full, err := s.cfg.Upstream.GrowthStreakFull(a)
	if err != nil {
		log.Printf("streak-bonus %s: %v", logfmt.Label(a.UID, a.Nickname), err)
		r.err = fmt.Errorf("查询连登失败：%w", err)
		return r
	}
	statuses := map[string]string{
		"7d":  full.RedemptionStatus.Tier7dStatus,
		"14d": full.RedemptionStatus.Tier14dStatus,
		"28d": full.RedemptionStatus.Tier28dStatus,
	}
	for _, tier := range full.RedemptionStatus.Tiers {
		status := statuses[tier.Tier]
		if status == "locked" || status == "claimed" {
			continue
		}
		if err := s.cfg.Upstream.GrowthRedeemTier(a, tier.Tier); err != nil {
			// 未解锁（403）属预期，静默；其余记日志。
			log.Printf("streak-bonus %s: redeem %s: %v", logfmt.Label(a.UID, a.Nickname), tier.Tier, err)
			continue
		}
		log.Printf("streak-bonus %s: ★ 兑换 %s 档（+%dc +%de 卡×%d 抽奖×%d）",
			a.UID, tier.Tier, tier.Credit, tier.Energy, tier.Cards, tier.Chances)
		r.parts = append(r.parts, fmt.Sprintf("兑换 %s 档 +%d", tier.Tier, tier.Credit))
		r.credit += int64(tier.Credit)
		r.energy += int64(tier.Energy)
	}
	// 抽奖：按当前 chances 全抽完（兑换刚发的次数已在服务端累加）。
	chances, err := s.cfg.Upstream.LotteryChances(a)
	if err != nil {
		log.Printf("streak-bonus %s: lottery summary: %v", logfmt.Label(a.UID, a.Nickname), err)
		r.err = fmt.Errorf("查询抽奖次数失败：%w", err)
		return r
	}
	drawn := 0
	for i := 0; i < chances; i++ {
		raw, err := s.cfg.Upstream.LotteryDraw(a)
		if err != nil {
			log.Printf("streak-bonus %s: draw: %v", logfmt.Label(a.UID, a.Nickname), err)
			r.err = fmt.Errorf("第 %d 次抽奖失败：%w", i+1, err)
			break
		}
		drawn++
		log.Printf("streak-bonus %s: 🎲 第%d抽 %s", logfmt.Label(a.UID, a.Nickname), i+1, compactJSON(raw))
	}
	if drawn > 0 {
		log.Printf("streak-bonus %s: 抽奖完成 %d 次", logfmt.Label(a.UID, a.Nickname), drawn)
		r.parts = append(r.parts, fmt.Sprintf("抽奖 %d 次", drawn))
	}
	return r
}

// compactJSON 裁剪奖品载荷（日志单行可读）。
func compactJSON(raw json.RawMessage) string {
	s := string(raw)
	if len(s) > 220 {
		return s[:220] + "…"
	}
	return s
}

// makeupYesterday 昨日漏签且有补签卡时自动补签（保住连登连续天数）。
// 无卡 / 无漏签 / 查询失败均静默（不影响主流程）。
func (s *Scheduler) makeupYesterday(a *auth.Auth) string {
	missed, err := s.cfg.Upstream.HeatmapYesterdayMissed(a)
	if err != nil || !missed {
		return ""
	}
	full, err := s.cfg.Upstream.GrowthStreakFull(a)
	if err != nil || full.MakeupCards.Balance <= 0 {
		return ""
	}
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	if err := s.cfg.Upstream.UseMakeupCard(a, yesterday); err != nil {
		log.Printf("streak-bonus %s: 补签 %s 失败: %v", logfmt.Label(a.UID, a.Nickname), yesterday, err)
		return ""
	}
	log.Printf("streak-bonus %s: ★ 已用补签卡补签 %s（保连登）", logfmt.Label(a.UID, a.Nickname), yesterday)
	return yesterday
}
