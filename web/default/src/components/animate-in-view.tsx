import { useRef, useEffect, useState, type ReactNode } from 'react'
import { cn } from '@/lib/utils'

interface AnimateInViewProps {
  children: ReactNode
  className?: string
  delay?: number
  threshold?: number
  animation?: 'fade-up' | 'fade-in' | 'scale-in' | 'fade-left' | 'fade-right'
  once?: boolean
  as?: 'div' | 'section' | 'li' | 'span'
}

export function AnimateInView(props: AnimateInViewProps) {
  const {
    as: Tag = 'div',
    delay = 0,
    threshold = 0.15,
    animation = 'fade-up',
    once = true,
  } = props

  const ref = useRef<HTMLDivElement>(null)
  const [shown, setShown] = useState(false)

  useEffect(() => {
    const el = ref.current
    if (!el) return

    const mq = window.matchMedia('(prefers-reduced-motion: reduce)')
    if (mq.matches) {
      setShown(true)
      return
    }

    const observer = new IntersectionObserver(
      ([entry]) => {
        if (entry.isIntersecting) {
          setShown(true)
          if (once) observer.unobserve(el)
        } else if (!once) {
          setShown(false)
        }
      },
      { threshold, rootMargin: '0px 0px -40px 0px' }
    )

    observer.observe(el)
    return () => observer.disconnect()
  }, [threshold, once, animation])

  return (
    <Tag
      ref={ref as never}
      className={cn(
        shown ? `landing-animate-${animation}` : 'opacity-0',
        props.className
      )}
      style={{ animationDelay: delay ? `${delay}ms` : undefined }}
    >
      {props.children}
    </Tag>
  )
}
