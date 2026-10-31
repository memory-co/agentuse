import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

export const fmtTime = (t?: number | null) => (t ? new Date(t * 1000).toLocaleString() : '');
export const errText = (e: unknown) => (e instanceof Error ? e.message : String(e));
