export type ImplementationId = "go" | "rust" | "zig";

export type Implementation = {
  id: ImplementationId;
  label: string;
  description: string;
  wsUrl: string;
  /** False until that language's actor server exists and is wired up. */
  available: boolean;
};

function envUrl(key: string, fallback: string): string {
  const value = import.meta.env[key] as string | undefined;
  return value && value.length > 0 ? value : fallback;
}

export const IMPLEMENTATIONS: Record<ImplementationId, Implementation> = {
  go: {
    id: "go",
    label: "Go",
    description: "Hollywood actors · Echo · port 8080",
    wsUrl: envUrl("VITE_WS_URL_GO", "ws://localhost:8080"),
    available: true,
  },
  rust: {
    id: "rust",
    label: "Rust",
    description: "Tokio actors · Axum · port 8090",
    wsUrl: envUrl("VITE_WS_URL_RUST", "ws://localhost:8090"),
    available: true,
  },
  zig: {
    id: "zig",
    label: "Zig",
    description: "Coming soon",
    wsUrl: envUrl("VITE_WS_URL_ZIG", "ws://localhost:8100"),
    available: false,
  },
};

export const IMPLEMENTATION_ORDER: ImplementationId[] = ["go", "rust", "zig"];

const STORAGE_KEY = "actor-chat.implementation";

export function loadImplementation(): ImplementationId | null {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (raw === "go" || raw === "rust" || raw === "zig") {
      if (IMPLEMENTATIONS[raw].available) return raw;
    }
  } catch {
    // private mode / blocked storage
  }
  return null;
}

export function saveImplementation(id: ImplementationId): void {
  try {
    localStorage.setItem(STORAGE_KEY, id);
  } catch {
    // ignore
  }
}

export function clearImplementation(): void {
  try {
    localStorage.removeItem(STORAGE_KEY);
  } catch {
    // ignore
  }
}
