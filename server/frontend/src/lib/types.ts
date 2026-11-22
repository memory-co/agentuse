import type { components } from '@/api/schema';

type DTO = components['schemas'];

export type Agent = DTO['Agent'];
export type AgentVersion = DTO['AgentVersion'];
export type Attachment = DTO['Attachment'];
export type Conversation = DTO['Conversation'];
export type ConversationRound = DTO['ConversationRound'];
export type ConversationStatus = Conversation['status'];
export type Message = DTO['Message'];
export type Question = DTO['Question'];
export type Run = DTO['Run'];
export type RunEventPreview = DTO['RunEventPreview'];
export type RunUsage = NonNullable<DTO['RunUsage']>;
export type Skill = DTO['Skill'];
export type SkillVersion = DTO['SkillVersion'];
export type User = DTO['User'];
export type Role = User['role'];

// The devices service normalizes empty arrays and keeps setup data out of the UI cache.
export type Device = Omit<DTO['Device'], 'capabilities' | 'harnesses' | 'setup_command' | 'setup_server_pin' | 'setup_token'> & {
  capabilities: NonNullable<DTO['Device']['capabilities']>;
  harnesses: NonNullable<DTO['Device']['harnesses']>;
};
