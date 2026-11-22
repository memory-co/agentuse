import { useMutation, useQuery } from '@tanstack/react-query';
import { api, apiData } from '@/api/client';
import type { components } from '@/api/schema';
import { queryClient } from '@/lib/query';
import type { Device } from '@/lib/types';

const listKey = ['devices'] as const;
export const deviceChangeQuery = { queryKey: listKey, exact: true };
const refresh = () => queryClient.invalidateQueries(deviceChangeQuery);

export const canUpgradeDevice = (device: Device) => device.status === 'online' && device.capabilities.includes('client-upgrade-v1');
export const isDeviceUpgrading = (device: Device) => device.client_upgrade?.status === 'downloading' || device.client_upgrade?.status === 'restarting';

export function useDevices(watchUpgrades = false) {
  return useQuery({
    queryKey: listKey,
    queryFn: async ({ signal }) => (await apiData(api.GET('/api/devices', { signal }))).map(deviceInfo),
    refetchInterval: query => watchUpgrades && query.state.data?.some(isDeviceUpgrading) ? 2000 : false,
  });
}

export function useClientRelease(enabled: boolean) {
  return useQuery({
    queryKey: ['client-release'],
    queryFn: ({ signal }) => apiData(api.GET('/api/devices/latest-release', { signal })),
    enabled,
    staleTime: 5 * 60_000,
    refetchInterval: 5 * 60_000,
  });
}

export function useHarnesses() {
  return useQuery({
    queryKey: ['harnesses'],
    queryFn: ({ signal }) => apiData(api.GET('/api/harnesses', { signal })),
    staleTime: 30_000,
  });
}

type DeviceDTO = components['schemas']['Device'];
type ReceiveSetupCommand = (id: string, command: string) => void;

function deviceInfo(dto: DeviceDTO): Device {
  const { setup_command: _command, setup_token: _token, setup_server_pin: _pin, ...device } = dto;
  return { ...device, capabilities: device.capabilities ?? [], harnesses: device.harnesses ?? [] };
}

async function publishDeviceSetup(device: DeviceDTO, receiveCommand: ReceiveSetupCommand) {
  const command = device.setup_command;
  const listed = deviceInfo(device);
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
      const device = await apiData(api.POST('/api/devices', { body: { name: name.trim() } }));
      await publishDeviceSetup(device, receiveCommand);
    },
    onSuccess: () => { void refresh(); },
  });
}

export function useResetDevice(id: string, receiveCommand: ReceiveSetupCommand) {
  return useMutation({
    mutationFn: async () => {
      const device = await apiData(api.POST('/api/devices/{id}/reset', { params: { path: { id } } }));
      await publishDeviceSetup(device, receiveCommand);
    },
    onSuccess: refresh,
  });
}

export function useDeleteDevice(id: string) {
  return useMutation({
    mutationFn: () => apiData(api.DELETE('/api/devices/{id}', { params: { path: { id } } })),
    onSuccess: () => { void refresh(); },
  });
}

export function useUpgradeDevice(id: string) {
  return useMutation({
    mutationFn: () => apiData(api.POST('/api/devices/{id}/upgrade', { params: { path: { id } } })),
    onSuccess: refresh,
  });
}

export function useSetDeviceHarness(id: string) {
  return useMutation({
    mutationFn: ({ harness, enabled }: { harness: string; enabled: boolean }) =>
      apiData(api.PUT('/api/devices/{id}/harnesses/{harness}', { params: { path: { id, harness } }, body: { enabled } })),
    onSuccess: () => { void refresh(); },
  });
}
