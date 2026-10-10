// Package credits 积分预测与选号策略（与具体平台无关）。
//
//   - Ledger：按账号、按天累计网关实测的积分消耗，得出消耗速度；
//   - Simulate：按到期顺序模拟消耗，预测每个积分包能用掉多少、浪费多少、何时耗尽；
//   - Service：周期刷新预测，向支持 RoutingTunable 的 Provider 注入选号权重，并记录选号决策。
package credits

import (
	"math"
	"sort"
	"time"

	"github.com/orangeruan/poolservice/internal/core/account"
)

const day = 24 * time.Hour

// Horizon 预测范围：更晚到期的积分包视为长期余额，不计入浪费与零浪费速度。
const Horizon = 90 * day

// PackageForecast 单个积分包的预测结果。
type PackageForecast struct {
	Name      string     `json:"name,omitempty"`
	Source    string     `json:"source,omitempty"`
	Amount    float64    `json:"amount"`
	Remaining float64    `json:"remaining"`
	ExpireAt  *time.Time `json:"expire_at,omitempty"`
	Used      float64    `json:"used"`  // 到期前预计能用掉的部分
	Waste     float64    `json:"waste"` // 到期时预计剩余（浪费）
}

// Sim 一组积分包在给定消耗速度下的预测。
type Sim struct {
	Packages         []PackageForecast `json:"packages,omitempty"`
	Waste            float64           `json:"waste"`
	FirstWasteAt     *time.Time        `json:"first_waste_at,omitempty"`
	ExhaustAt        *time.Time        `json:"exhaust_at,omitempty"`
	RequiredPerDay   float64           `json:"required_per_day"` // 零浪费所需日均消耗
	Expiring24h      float64           `json:"expiring_24h"`
	Expiring72h      float64           `json:"expiring_72h"`
	Expiring7d       float64           `json:"expiring_7d"`
	Expiring30d      float64           `json:"expiring_30d"`
	NextExpireAt     *time.Time        `json:"next_expire_at,omitempty"`
	NextExpireAmount float64           `json:"next_expire_amount"`
	NoExpiry         float64           `json:"no_expiry"` // 不过期（或到期时间未知）的余额
}

// normalizePackages 以余额为准校正积分包明细：
//   - 去掉已过期 / 已用完的包；
//   - 明细合计 > 余额（明细刷新滞后于实时扣费）：差额从最早到期的包扣除（上游按到期先后扣费）；
//   - 明细合计 < 余额（明细缺失）：差额视为不过期余额。
func normalizePackages(pkgs []account.Package, remaining float64, now time.Time) []account.Package {
	out := make([]account.Package, 0, len(pkgs)+1)
	var sum float64
	for _, p := range pkgs {
		if p.Remaining <= 0 || p.Expired(now) {
			continue
		}
		out = append(out, p)
		sum += p.Remaining
	}
	sortByExpiry(out)
	if remaining < 0 {
		remaining = 0
	}
	switch {
	case sum > remaining+0.5:
		diff := sum - remaining
		for i := range out {
			if diff <= 0 {
				break
			}
			take := math.Min(diff, out[i].Remaining)
			out[i].Remaining -= take
			diff -= take
		}
		kept := out[:0]
		for _, p := range out {
			if p.Remaining > 0.0001 {
				kept = append(kept, p)
			}
		}
		out = kept
	case remaining > sum+0.5:
		out = append(out, account.Package{Name: "其他余额", Source: "other", Amount: remaining - sum, Remaining: remaining - sum})
	}
	return out
}

func sortByExpiry(ps []account.Package) {
	sort.SliceStable(ps, func(i, j int) bool {
		a, b := ps[i].ExpireAt, ps[j].ExpireAt
		switch {
		case a == nil && b == nil:
			return false
		case a == nil:
			return false
		case b == nil:
			return true
		}
		return a.Before(*b)
	})
}

func ptr(t time.Time) *time.Time { return &t }

// Simulate 假设以 burnPerDay 匀速消耗、按到期先后扣费，预测各积分包的使用与浪费。
func Simulate(pkgs []account.Package, remaining, burnPerDay float64, now time.Time) Sim {
	ps := normalizePackages(pkgs, remaining, now)
	var s Sim
	var consumed, cum float64
	for _, p := range ps {
		f := PackageForecast{Name: p.Name, Source: p.Source, Amount: p.Amount, Remaining: p.Remaining, ExpireAt: p.ExpireAt}
		if p.ExpireAt == nil || p.ExpireAt.Sub(now) > Horizon {
			// 不过期，或到期远超预测范围：速度估计在这么长的跨度上没有意义，视为长期余额。
			f.Used = p.Remaining
			s.NoExpiry += p.Remaining
		} else {
			left := p.ExpireAt.Sub(now)
			days := left.Hours() / 24
			capacity := burnPerDay*days - consumed
			f.Used = math.Max(0, math.Min(p.Remaining, capacity))
			f.Waste = p.Remaining - f.Used
			if f.Waste < 0.01 {
				f.Waste = 0
			}
			if f.Waste > 0 && s.FirstWasteAt == nil {
				s.FirstWasteAt = ptr(*p.ExpireAt)
			}
			s.Waste += f.Waste
			cum += p.Remaining
			if req := cum / math.Max(days, 1.0/24); req > s.RequiredPerDay {
				s.RequiredPerDay = req
			}
			if s.NextExpireAt == nil {
				s.NextExpireAt = ptr(*p.ExpireAt)
			}
			if p.ExpireAt.Equal(*s.NextExpireAt) || sameDay(*p.ExpireAt, *s.NextExpireAt) {
				s.NextExpireAmount += p.Remaining
			}
			switch {
			case left <= day:
				s.Expiring24h += p.Remaining
				fallthrough
			case left <= 3*day:
				s.Expiring72h += p.Remaining
				fallthrough
			case left <= 7*day:
				s.Expiring7d += p.Remaining
				fallthrough
			case left <= 30*day:
				s.Expiring30d += p.Remaining
			}
		}
		consumed += f.Used
		s.Packages = append(s.Packages, f)
	}
	if burnPerDay > 0 && consumed > 0 {
		s.ExhaustAt = ptr(now.Add(time.Duration(consumed / burnPerDay * float64(day))))
	}
	return s
}

func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

// CalendarDay 到期日历的一天。
type CalendarDay struct {
	Date     string  `json:"date"` // 2006-01-02（本地时区）
	Expiring float64 `json:"expiring"`
	Waste    float64 `json:"waste"`
	Packages int     `json:"packages"`
}

// Calendar 未来 days 天按到期日汇总（含今天）。
func Calendar(sims []Sim, now time.Time, days int) []CalendarDay {
	out := make([]CalendarDay, days)
	idx := map[string]int{}
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	for i := range out {
		d := start.AddDate(0, 0, i).Format("2006-01-02")
		out[i].Date = d
		idx[d] = i
	}
	for _, s := range sims {
		for _, p := range s.Packages {
			if p.ExpireAt == nil {
				continue
			}
			if i, ok := idx[p.ExpireAt.In(now.Location()).Format("2006-01-02")]; ok {
				out[i].Expiring += p.Remaining
				out[i].Waste += p.Waste
				out[i].Packages++
			}
		}
	}
	return out
}
