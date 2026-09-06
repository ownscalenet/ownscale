import { Link } from 'react-router-dom'
import { usePageTitle } from '../hooks/usePageTitle'

// Rendered for any URL that doesn't match a known route (typo, stale
// bookmark, removed page), instead of the blank content area React
// Router leaves behind when nothing matches.
export default function NotFound() {
  usePageTitle('Not found')
  return (
    <div className="hero min-h-screen bg-base-200">
      <div className="hero-content text-center">
        <div className="max-w-md">
          <h1 className="text-3xl font-bold mb-2">Page not found</h1>
          <p className="mb-6 text-base-content/70">
            The page you&rsquo;re looking for doesn&rsquo;t exist.
          </p>
          <Link to="/" className="btn btn-primary">
            Go home
          </Link>
        </div>
      </div>
    </div>
  )
}
