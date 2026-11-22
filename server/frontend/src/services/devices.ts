import { useMutation, useQuery } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { queryClient } from '@/lib/query';
import type { ClientUpgrade, Device, Harness } from '@/lib/types';

const listKey = ['devices'] as const;
const devicePath = (id: string) => `/api/devices/${encodeURIComponent(id)}`;
export const deviceChangeQuery = { queryKey: listKey, exact: true };
const refresh = () => queryClient.invalidateQueries(deviceChangeQuery);

export const canUpgradeDevice = (device: Device) => device.status === 'online' && device.capabilities.includes('client-upgrade-v1');
export const isDeviceUpgrading = (device: Device) => device.client_upgrade?.status === 'downloading' || device.client_upgrade?.status === 'restarting';

export function useDevices(watchUpgrades = false) {
  return useQuery({
    queryKey: listKey,
    queryFn: ({ signal }) => api<Device[]>('/api/devices', { signal }),
    refetchInterval: query => watchUpgrades && query.state.data?.some(isDeviceUpgrading) ? 2000 : false,
  });
}

export function useClientRelease(enabled: boolean) {
  return useQuery({
    queryKey: ['client-release'],
    queryFn: ({ signal }) => api<{ version: string }>('/api/devices/latest-release', { signal }),
    enabled,
    staleTime: 5 * 60_000,
    refetchInterval: 5 * 60_000,
  });
}

export function useHarnesses() {
  return useQuery({
    queryKey: ['harnesses'],
    queryFn: ({ signal }) => api<Harness[]>('/api/harnesses', { signal }),
    staleTime: 30_000,
  });
}

type DeviceSetup = Device & { setup_command?: string; setup_token?: string };
type ReceiveSetupCommand = (id: string, command: string) => void;

async function publishDeviceSetup(device: DeviceSetup, receiveCommand: ReceiveSetupCommand) {
  const { setup_command: command, setup_token: _token, ...listed } = device;
  // Store the pending device first, then hand the command to its page.
  // Both mutations return void so their result cache holds no credentials.
  await queryClient.cancelQueries(deviceChangeQuery);
  queryClient.setQueryData<Device[]>(listKey, current => current?.some(item => item.id === listed.id)
    ? current.map(item => item.id === listed.id ? listed : item)
    : [...(current ?? []), listed]);
  if (command) receiveCommand(listed.id, command);
}

export function useCreateDevice(receiveCommand: ReceiveSetupCommand) {
  return useMutation({
    mutationFn: async (name: string) => {
      const device = await api<DeviceSetup>('/api/devices', { method: 'POST', body: { name: name.trim() } });
      await publishDeviceSetup(device, receiveCommand);
    },
    onSuccess: () => { void refresh(); },
  });
}

export function useResetDevice(id: string, receiveCommand: ReceiveSetupCommand) {
  return useMutation({
    mutationFn: async () => {
      const device = await api<DeviceSetup>(`${devicePath(id)}/reset`, { method: 'POST' });
      await publishDeviceSetup(device, receiveCommand);
    },
    onSuccess: refresh,
  });
}

export function useDeleteDevice(id: string) {
  return useMutation({
    mutationFn: () => api(devicePath(id), { method: 'DELETE' }),
    onSuccess: () => { void refresh(); },
  });
}

export function useUpgradeDevice(id: string) {
  return useMutation({
    mutationFn: () => api<ClientUpgrade>(`${devicePath(id)}/upgrade`, { method: 'POST' }),
    onSuccess: refresh,
  });
}

export function useSetDeviceHarness(id: string) {
  return useMutation({
    mutationFn: ({ harness, enabled }: { harness: string; enabled: boolean }) =>
      api(`${devicePath(id)}/harnesses/${encodeURIComponent(harness)}`, { method: 'PUT', body: { enabled } }),
    onSuccess: () => { void refresh(); },
  });
}
