import type { AgentId } from "$lib/domain/agent.types";
export interface HealthItem { name: string; state: string; }
export interface Pref { key: string; icon: string; name: string; desc: string; kind: "toggle" | "select"; value?: string; dbPath?: string; dbSize?: string; }
export type ConnState = "on" | "idle" | "off";
export interface ConnectionRow { id: AgentId; seen: string; state: ConnState; }
export interface CollectionItem { icon: string; label: string; key: string; sent: boolean; }