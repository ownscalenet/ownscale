import { useEffect, useState } from "react";
import { ApiError, createOrganization } from "../api";
import { useAuth } from "../auth";

// How often to re-check the organization's provisioning status once it's
// been created. Provisioning normally finishes in a few seconds (it's
// picked up by the background provisioner right away, not on its own poll
// interval), so a short interval keeps the wait feeling responsive without
// hammering the server.
const STATUS_POLL_INTERVAL_MS = 1500;

export default function CreateOrganization() {
  const { organization, refresh } = useAuth();
  const [subdomain, setSubdomain] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  // Once the organization exists, poll GET /api/session (via refresh())
  // until its status stops being "pending". Creating the org's Docker
  // network is what makes the browser drop in-flight requests, so we
  // deliberately don't keep a request open across that -- we just poll a
  // fresh, short-lived request instead.
  useEffect(() => {
    if (organization?.Status !== "pending") {
      return;
    }
    const id = setInterval(() => {
      void refresh();
    }, STATUS_POLL_INTERVAL_MS);
    return () => clearInterval(id);
  }, [organization?.Status, refresh]);

  useEffect(() => {
    if (organization?.Status === "active") {
      window.location.href = `https://${organization.Subdomain}.${window.location.host}/admin/oidc/start`;
    }
  }, [organization?.Status, organization?.Subdomain]);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setSubmitting(true);
    try {
      await createOrganization(subdomain.trim());
      await refresh();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : String(err));
    } finally {
      setSubmitting(false);
    }
  }

  if (organization?.Status === "pending") {
    return (
      <div className="hero min-h-screen bg-base-200">
        <div className="hero-content text-center">
          <div className="max-w-md w-full">
            <span className="loading loading-spinner loading-lg mb-4"></span>
            <h1 className="text-3xl font-bold mb-2">Setting up your organization…</h1>
            <p className="text-base-content/70">
              This usually only takes a few seconds. This page will move on
              automatically once <code>{organization.Subdomain}</code> is ready.
            </p>
          </div>
        </div>
      </div>
    );
  }

  if (organization?.Status === "failed") {
    return (
      <div className="hero min-h-screen bg-base-200">
        <div className="hero-content text-center">
          <div className="max-w-md w-full">
            <h1 className="text-3xl font-bold mb-2">Something went wrong</h1>
            <div role="alert" className="alert alert-error">
              <span>
                We couldn't finish setting up <code>{organization.Subdomain}</code>. Please
                contact an administrator for help.
              </span>
            </div>
          </div>
        </div>
      </div>
    );
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
