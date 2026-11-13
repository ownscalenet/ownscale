// Applies the saved theme before first paint to avoid a flash. It is a
// separate file rather than an inline script because the CSP forbids inline
// scripts.
try {
  var t = localStorage.getItem('ownscale-theme')
  if (t === 'light' || t === 'dark') document.documentElement.dataset.theme = t
} catch {}
