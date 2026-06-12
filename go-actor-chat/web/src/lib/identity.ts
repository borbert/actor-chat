// Dev-mode identity persisted in localStorage. Replaced by WorkOS AuthKit
// in M3.
export type Identity = {
  userId: string;
  name: string;
};

const KEY = "actor-chat-identity";

export function loadIdentity(): Identity | null {
  const raw = localStorage.getItem(KEY);
  if (!raw) return null;
  try {
    const parsed = JSON.parse(raw) as Identity;
    if (parsed.userId && parsed.name) return parsed;
  } catch {
    // fall through to null
  }
  return null;
}

export function saveIdentity(identity: Identity) {
  localStorage.setItem(KEY, JSON.stringify(identity));
}

export function clearIdentity() {
  localStorage.removeItem(KEY);
}
