// blackcat.go 夜猫子任务执行器：23:00–08:00 窗口内对池内账号补足 glm-5.2 对话
// 并上报事件链（black_cat 判据）。窗口外触发则直接跳过（只观测，不报错）。
package scheduler

import (
	"fmt"
	"log"
	"time"

	"github.com/orangeruan/poolservice/internal/providers/workbuddy/engine/logfmt"
	"github.com/orangeruan/poolservice/internal/providers/workbuddy/engine/upstream"
)

// nowForNightWindow 夜猫子窗口判定所用时钟（生产恒为 time.Now）。
//
// 之所以留一个可替换点：窗口守卫**先于**账号循环，若不钉住时钟，「企业号门控是否生效」
// 这条断言的判别力就完全取决于运行环境的墙钟——本机白天跑等于该用例什么都没测，
// 只有 CI 恰好落在 UTC 夜里才真正执行。属典型的「随环境静默失效」的测试。
var nowForNightWindow = time.Now

// RunBlackcatNow 对所有可用账号执行夜猫子对话补足（窗口外跳过）。
// 由 blackcat_hours 排程（默认 [23]）触发；执行前二次校验 InNightWindow。
func (s *Scheduler) RunBlackcatNow() { s.runBlackcat(nil, TriggerSchedule) }

// RunBlackcatFor 手动触发，只处理 only 中的账号（nil = 全部；显式指定时禁用账号也执行）。
func (s *Scheduler) RunBlackcatFor(only []string) { s.runBlackcat(only, TriggerManual) }

func (s *Scheduler) runBlackcat(only []string, trigger string) {
	rep := s.begin("blackcat", only, trigger)
	defer rep.End()
	if !upstream.InNightWindow(nowForNightWindow()) {
		log.Printf("blackcat: 当前不在 23:00–08:00 计数窗口，跳过")
		rep.Note("当前不在 23:00–08:00 计数窗口，未执行")
		return
	}
	for _, st := range s.listFor(only) {
		// 暂停选号（paused）账号跳过：夜猫子是全任务体系中唯一「整任务都是
		// 真实模型对话」的（RunNightChats 逐条 ChatStream 发 glm-5.2 短对话），
		// 与「让位防风控」正面冲突。旅行/成长任务都是纯上报或领奖 RPC，照常跑。
		if st.Disabled && only == nil {
			continue
		}
		if st.Paused {
			rep.Item(st.UID, st.Nickname, StatusSkipped, "账号已暂停选号，不做真实对话", 0, 0)
			continue
		}
		a := s.cfg.Pool.AuthByUID(st.UID)
		if a == nil || a.AccessTokenValue() == "" {
			rep.Item(st.UID, st.Nickname, StatusSkipped, "凭证缺少 access token", 0, 0)
			continue
		}
		if a.IsGlobal() {
			rep.Item(st.UID, st.Nickname, StatusSkipped, "国际版没有夜猫子任务", 0, 0)
			continue // D4 门控：global 无 CN 任务体系，不发起任何上游调用
		}
		if a.IsEnterprise() {
			rep.Item(st.UID, st.Nickname, StatusSkipped, "企业版没有成长体系", 0, 0)
			continue // 企业版门控：无成长体系（growth 403 / claim-gift 400「企业账号不支持该操作」）
		}
		need, err := s.cfg.Upstream.BlackcatNeed(a)
		if err != nil {
			log.Printf("blackcat %s: %v", logfmt.Label(a.UID, a.Nickname), err)
			rep.Item(st.UID, st.Nickname, StatusFailed, errMsg("查询进度失败", err), 0, 0)
			continue
		}
		if need <= 0 {
			rep.Item(st.UID, st.Nickname, StatusSkipped, "今日进度已达标", 0, 0)
			continue
		}
		ok, err := s.cfg.Upstream.RunNightChats(a, int(need))
		if err != nil {
			log.Printf("blackcat %s: %d/%d 完成，中断: %v", logfmt.Label(a.UID, a.Nickname), ok, need, err)
			rep.Item(st.UID, st.Nickname, StatusFailed, fmt.Sprintf("完成 %d/%d 次后中断：%v", ok, need, err), 0, 0)
			continue
		}
		log.Printf("blackcat %s: 完成 %d 次夜间对话", logfmt.Label(a.UID, a.Nickname), ok)
		rep.Item(st.UID, st.Nickname, StatusOK, fmt.Sprintf("完成 %d 次夜间对话", ok), 0, 0)
		time.Sleep(activityAccountDelay)
	}
}
