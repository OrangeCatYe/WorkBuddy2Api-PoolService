import { Construction } from 'lucide-react'
import { Card, Empty } from '@/components/ui'

export function Placeholder({ phase, title, items }: { phase: string; title: string; items: string[] }) {
  return (
    <Card>
      <Empty
        icon={<Construction size={30} />}
        title={`${title} · 规划于 ${phase}`}
        hint={
          <span className="mt-1 block text-left">
            {items.map((i) => (
              <span key={i} className="block">
                · {i}
              </span>
            ))}
          </span>
        }
      />
    </Card>
  )
}
