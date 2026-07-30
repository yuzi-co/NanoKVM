export type HidDeviceStatus = {
  name: string;
  path: string;
  state: 'unknown' | 'accepting' | 'stalled' | 'error';
  detail?: string;
  stateForMs: number;
  observedMsAgo: number;
};

export const ABSOLUTE_MOUSE = 'mouse-absolute';

// How old an observation may be and still count. The server only learns the
// state of an endpoint by writing to it, so a stall stops being refreshed as
// soon as the operator stops moving the mouse. Warning about an observation
// older than this would keep a fault on screen long after it may have cleared.
const FRESH_MS = 60_000;

// isAbsoluteMouseStalled reports whether absolute mouse reports are currently
// going nowhere.
//
// A target can stop collecting from one HID interface while it keeps collecting
// from the others, so the pointer stops moving and the keyboard keeps typing.
// Nothing else in the system reports this: the device node is present and the
// USB gadget is bound, so every other signal reads healthy.
//
// Why a target does it is not knowable from here. Both remedies the UI offers
// have been seen to work, which is why it offers both rather than picking one.
export function isAbsoluteMouseStalled(devices: HidDeviceStatus[]): boolean {
  const absolute = devices.find((device) => device.name === ABSOLUTE_MOUSE);
  if (!absolute) return false;

  return absolute.state === 'stalled' && absolute.observedMsAgo < FRESH_MS;
}
