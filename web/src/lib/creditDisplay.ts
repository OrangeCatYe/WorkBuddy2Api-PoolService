import { useEffect, useState } from 'react'

/**
 * 积分显示精度偏好。
 *
 * 上游积分以「个」为单位，单次消耗常常只有几分到十几分，
 * 折算成「万」会把这些数字全部压成 0，故默认直接显示到个位。
 * 需要看清小数（如核对上游账单）时可切到两位小数。
 *
 * 这是纯前端显示偏好，不进后端配置，存 localStorage。
 */
export type CreditPrecision = 'int' | 'two'

const KEY = 'poolsvc.creditPrecision'

function read(): CreditPrecision {
  return localStorage.getItem(KEY) === 'two' ? 'two' : 'int'
}

// 模块级缓存 + 订阅者：fmtNum 是纯函数、被大量组件调用，
// 不能每次都读 localStorage，否则也无法在切换后触发重渲染。
let current: CreditPrecision = 'int'
try {
  current = read()
} catch {
  current = 'int'
}
const listeners = new Set<(p: CreditPrecision) => void>()

export function getCreditPrecision(): CreditPrecision {
  return current
}

export function setCreditPrecision(p: CreditPrecision) {
  current = p
  try {
    localStorage.setItem(KEY, p)
  } catch {
    // 隐私模式下写入失败时，本次会话内仍然生效
  }
  listeners.forEach((fn) => fn(p))
}

/** 组件内订阅精度变化；切换开关后所有显示积分的位置一起重渲染。 */
export function useCreditPrecision(): CreditPrecision {
  const [p, set] = useState<CreditPrecision>(current)
  useEffect(() => {
    listeners.add(set)
    return () => {
      listeners.delete(set)
    }
  }, [])
  return p
}
