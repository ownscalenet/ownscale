import { useCallback, useEffect, useState } from 'react'

export type Theme = 'system' | 'light' | 'dark'

const storageKey = 'ownscale-theme'

function stored(): Theme {
  const t = localStorage.getItem(storageKey)
  return t === 'light' || t === 'dark' ? t : 'system'
}

// apply sets data-theme; for "system" it's removed so daisyUI follows
// prefers-color-scheme.
function apply(theme: Theme) {
  if (theme === 'system') delete document.documentElement.dataset.theme
  else document.documentElement.dataset.theme = theme
}

// useTheme returns the saved theme and a setter that persists and applies it.
export function useTheme(): [Theme, (t: Theme) => void] {
  const [theme, setThemeState] = useState<Theme>(stored)

  useEffect(() => apply(theme), [theme])

  const setTheme = useCallback((t: Theme) => {
    if (t === 'system') localStorage.removeItem(storageKey)
    else localStorage.setItem(storageKey, t)
    setThemeState(t)
  }, [])

  return [theme, setTheme]
}
