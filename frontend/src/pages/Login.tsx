export default function Login() {
  return (
    <div className="hero min-h-screen bg-base-200">
      <div className="hero-content text-center">
        <div className="max-w-md">
          <h1 className="text-3xl font-bold mb-2">Admin Panel</h1>
          <p className="mb-6 text-base-content/70">Sign in to continue.</p>
          <a href="/auth/google" className="btn btn-primary">
            Sign in with Google
          </a>
        </div>
      </div>
    </div>
  )
}
