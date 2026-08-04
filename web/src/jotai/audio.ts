import { atom } from 'jotai';

// Audio starts muted. Browsers refuse to autoplay sound before the user acts,
// so the first unmute has to be a click.
export const audioMutedAtom = atom(true);

// hasAudio follows the peer connection's ontrack event, which is the only
// availability signal there is. The server offers a PCMU track when the USB
// audio gadget has a capture card, and offers nothing when it does not, so a
// track that arrives means the operator can hear the host, and no track means
// the feature is off on this device.
//
// The speaker button is gated on this. Without it the button renders on every
// device, and on the great majority - which have no /boot/usb.uac marker -
// clicking unmute sets muted = false on an <audio> with no srcObject and
// produces silence with nothing to explain it.
export const hasAudioAtom = atom(false);
