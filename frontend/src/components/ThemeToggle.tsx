import { Monitor, Moon, Sun } from 'lucide-react'
import { useTheme, type Theme } from '../hooks/useTheme'

const next: Record<Theme, Theme> = { system: 'light', light: 'dark', dark: 'system' }
const label: Record<Theme, string> = { system: 'System', light: 'Light', dark: 'Dark' }
const icon: Record<Theme, typeof Sun> = { system: Monitor, light: Sun, dark: Moon }

// ThemeToggle cycles the color theme: system, light, dark.
export default function ThemeToggle() {
  const [theme, setTheme] = useTheme()
  const Icon = icon[theme]
  return (
    <button
      type="button"
      className="btn btn-ghost btn-sm btn-square"
      aria-label={`Theme: ${label[theme]}. Switch to ${label[next[theme]]}`}
      title={`Theme: ${label[theme]}`}
      onClick={() => setTheme(next[theme])}
    >
      <Icon className="size-4" />
    </button>
  )
}
