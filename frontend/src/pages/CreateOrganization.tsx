import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { ApiError, createOrganization } from "../api";
import { useAuth } from "../auth";

export default function CreateOrganization() {
  const { refresh } = useAuth();
  const navigate = useNavigate();
  const [subdomain, setSubdomain] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setSubmitting(true);
    try {
      await createOrganization(subdomain.trim());
      await refresh();
      navigate("/", { replace: true });
    } catch (err) {
      setError(err instanceof ApiError ? err.message : String(err));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="hero min-h-screen bg-base-200">
      <div className="hero-content text-center">
        <div className="max-w-md w-full">
          <h1 className="text-3xl font-bold mb-2">Create your organization</h1>
          <p className="mb-6 text-base-content/70">
            Choose a subdomain for your organization. It will be available at{" "}
            <code>&lt;subdomain&gt;.{window.location.host}</code>.
          </p>
          <form onSubmit={handleSubmit} className="form-control gap-4 text-left">
            <label className="form-control w-full">
              <div className="label">
                <span className="label-text">Subdomain</span>
              </div>
              <input
                type="text"
                className="input input-bordered w-full"
                placeholder="acme"
                value={subdomain}
                onChange={(e) => setSubdomain(e.target.value)}
                disabled={submitting}
                autoFocus
                required
              />
            </label>
            {error && (
              <div role="alert" className="alert alert-error">
                <span>{error}</span>
              </div>
            )}
            <button
              type="submit"
              className="btn btn-primary"
              disabled={submitting || subdomain.trim() === ""}
            >
              {submitting ? (
                <span className="loading loading-spinner loading-sm"></span>
              ) : (
                "Create organization"
              )}
            </button>
          </form>
        </div>
      </div>
    </div>
  );
}
