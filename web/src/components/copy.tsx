import { useState } from 'react'
import { Check, Copy } from 'lucide-react'
import { cx } from './ui'

async function copyText(text: string) {
  try {
    await navigator.clipboard.writeText(text)
  } catch {
    // 非安全上下文（http 局域网访问）时 clipboard API 不可用，退回 execCommand
    const ta = document.createElement('textarea')
    ta.value = text
    ta.style.position = 'fixed'
    ta.style.opacity = '0'
    document.body.appendChild(ta)
    ta.select()
    document.execCommand('copy')
    ta.remove()
  }
}

export function CopyButton({ text, className, label }: { text: string; className?: string; label?: string }) {
  const [done, setDone] = useState(false)
  return (
    <button
      type="button"
      onClick={async (e) => {
        e.stopPropagation()
        await copyText(text)
        setDone(true)
        setTimeout(() => setDone(false), 1400)
      }}
      className={cx('inline-flex items-center gap-1 rounded-md px-1.5 py-1 text-[12px] text-faint transition-colors hover:bg-sunken hover:text-ink', className)}
      aria-label="复制"
    >
      {done ? <Check size={13} className="text-ok" /> : <Copy size={13} />}
      {label && <span>{done ? '已复制' : label}</span>}
    </button>
  )
}

/** 带复制按钮的代码块。 */
export function CodeBlock({ code, lang }: { code: string; lang?: string }) {
  return (
    <div className="group relative rounded-xl border border-line bg-sunken/70">
      {lang && <div className="eyebrow border-b border-line px-3.5 py-1.5 !text-[10.5px]">{lang}</div>}
      <pre className="num overflow-x-auto px-3.5 py-3 text-[12px] leading-relaxed text-ink">{code}</pre>
      <CopyButton text={code} className="absolute right-2 top-1.5 bg-surface/80" label="复制" />
    </div>
  )
}
