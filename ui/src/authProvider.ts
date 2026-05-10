import type { AuthBindings } from "@refinedev/core";
import { UserManager, WebStorageStateStore } from "oidc-client-ts";
import { ZITADEL_AUTHORITY, ZITADEL_CLIENT_ID } from "@/config";

export const userManager = new UserManager({
  authority: ZITADEL_AUTHORITY,
  client_id: ZITADEL_CLIENT_ID,
  redirect_uri: `${window.location.origin}/auth/callback`,
  post_logout_redirect_uri: window.location.origin,
  silent_redirect_uri: `${window.location.origin}/auth/silent`,
  scope: "openid profile email",
  response_type: "code",
  automaticSilentRenew: true,
  userStore: new WebStorageStateStore({ store: window.localStorage }),
});

export const authProvider: AuthBindings = {
  login: async () => {
    await userManager.signinRedirect();
    return { success: false }; // redirect takes over
  },

  logout: async () => {
    await userManager.signoutRedirect();
    return { success: true, redirectTo: "/login" };
  },

  check: async () => {
    const user = await userManager.getUser();
    if (user && !user.expired) {
      return { authenticated: true };
    }
    return {
      authenticated: false,
      redirectTo: "/login",
      error: { name: "Unauthorized", message: "Not authenticated" },
    };
  },

  onError: async (error) => {
    if (error?.response?.status === 401) {
      return { logout: true, redirectTo: "/login" };
    }
    return { error };
  },

  getIdentity: async () => {
    const user = await userManager.getUser();
    if (!user?.profile) return null;
    return {
      id: user.profile.sub,
      name: user.profile.name ?? user.profile.preferred_username ?? user.profile.email,
      email: user.profile.email,
      avatar: user.profile.picture,
    };
  },
};
