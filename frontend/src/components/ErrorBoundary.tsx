import { Component, type ErrorInfo, type ReactNode } from 'react'

interface Props {
  children: ReactNode
}

interface State {
  error: Error | null
}

// Catches render/lifecycle errors anywhere below it in the tree so a bug in
// one page doesn't whitescreen the entire console with no way to recover.
export class ErrorBoundary extends Component<Props, State> {
  state: State = { error: null }

  static getDerivedStateFromError(error: Error): State {
    return { error }
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error('Unhandled error in the console UI:', error, info.componentStack)
  }

  render() {
    if (this.state.error) {
      return (
        <div className="hero min-h-screen bg-base-200">
          <div className="hero-content text-center">
            <div className="max-w-md">
              <h1 className="text-3xl font-bold mb-2">Something went wrong</h1>
              <p className="mb-6 text-base-content/70">
                {this.state.error.message || 'An unexpected error occurred.'}
              </p>
              <button
                type="button"
                className="btn btn-primary"
                onClick={() => {
                  this.setState({ error: null })
                  window.location.assign('/')
                }}
              >
                Reload
              </button>
            </div>
          </div>
        </div>
      )
    }
    return this.props.children
  }
}
