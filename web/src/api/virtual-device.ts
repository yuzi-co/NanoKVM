import { http } from '@/lib/http.ts';

export type VirtualDeviceName = 'console' | 'disk' | 'network' | 'audio';

// enabled is the /boot marker, active is the function the running gadget
// actually carries. They differ when the USB controller ran out of endpoints.
export type VirtualDeviceState = {
  enabled: boolean;
  active: boolean;
  cost: number;
};

export type VirtualDevices = {
  console: VirtualDeviceState;
  network: VirtualDeviceState;
  disk: VirtualDeviceState;
  audio: VirtualDeviceState;
  used: number;
  total: number;
};

// get virtual devices status
export function getVirtualDevice() {
  return http.get('/api/vm/device/virtual');
}

// mount/unmount virtual device
export function updateVirtualDevice(device: VirtualDeviceName) {
  const data = {
    device
  };

  return http.post('/api/vm/device/virtual', data);
}
