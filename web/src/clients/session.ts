import { apiAddress } from "../config/api";
import { err, ok, type Result } from "../utils/result";
import { fetchJSON, send } from "./http";

/**
 * The identity behind the current request, as the API publishes it
 * (`session.v1`, see internal/httpapi/auth.go).
 *
 * It carries identity and display fields and no authority of any kind: this hub
 * has no roles, so there is nothing here for a surface to branch on.
 */
export interface PrincipalDTO {
  id: string;
  issuer: string;
  subject: string;
  name: string;
  email?: string;
}

interface SessionDTO {
  principal: PrincipalDTO;
}

/** Who the hub believes the operator is, or a failure. */
export async function loadPrincipal(): Promise<Result<PrincipalDTO, Error>> {
  const session = await fetchJSON<SessionDTO>("/auth/session");
  if (!session.ok) return err(session.error);
  return ok(session.value.principal);
}

/** Ends the session on the server, which takes effect on the next request. */
export async function endSession(): Promise<Result<void, Error>> {
  return send("/auth/session", "DELETE");
}

/**
 * Where the browser goes to sign in, carrying where it wants to come back to.
 *
 * The destination is a path inside this application, fragment included: the hub
 * is one document with a fragment route, so "/#/places/x" is what returns the
 * operator to the page they asked for. The server narrows anything else to "/",
 * so this can never become an open redirect.
 */
export function signInAddress(returnTo: string): string {
  return apiAddress(`/auth/sign-in?return=${encodeURIComponent(returnTo)}`);
}
