import { useEffect } from "react";
import { useAuth } from "../auth";

// How often to re-check the organization's provisioning status once it's
// been created. Provisioning normally finishes in a few seconds (it's
// picked up by the background provisioner right away, not on its own poll
// interval), so a short interval keeps the wait feeling responsive without
// hammering the server.
const STATUS_POLL_INTERVAL_MS = 1500;

export default function CreateOrganization() {
  const { organization, refresh } = useAuth();

  // Once the organization exists, poll GET /api/session (via refresh())
  // until its status stops being "pending". Creating the org's Docker
  // network is what makes the browser drop in-flight requests, so we
  // deliberately don't keep a request open across that.
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
      window.location.href = `https://${organization.Domain}/admin/oidc/start`;
    }
  }, [organization?.Status, organization?.Domain]);

  if (organization?.Status === "failed") {
    return (
      <div className="hero min-h-screen bg-base-200">
        <div className="hero-content text-center">
          <div className="max-w-md w-full">
            <h1 className="text-3xl font-bold mb-2">Something went wrong</h1>
            <div role="alert" className="alert alert-error">
              <span>
                We couldn't finish setting up <code>{organization.Domain}</code>. Please
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
          <span className="loading loading-spinner loading-lg mb-4"></span>
          <h1 className="text-3xl font-bold mb-2">Setting up your organization…</h1>
          <p className="text-base-content/70">
            This usually only takes a few seconds. This page will move on automatically
            once your organization is ready.
          </p>
        </div>
      </div>
    </div>
  );
}
