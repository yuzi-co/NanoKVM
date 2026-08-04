import { atom } from 'jotai';

// Audio starts muted. Browsers refuse to autoplay sound before the user acts,
// so the first unmute has to be a click.
export const audioMutedAtom = atom(true);
