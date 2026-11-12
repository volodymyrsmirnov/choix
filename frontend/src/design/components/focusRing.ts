// The explicit keyboard focus ring for controls styled with utilities (the
// component classes in base.css carry their own): a solid 2 px outline,
// offset 2 px, transparent until :focus-visible. Never `outline-style: auto`.
//
// Where the ring is drawn by an ancestor instead of the focused control (an
// ancestor styled with :focus-within, such as TextInput's bordered box), that
// ancestor carries the `data-focus-ring` attribute. The focus check compares
// the focused control, or its nearest `[data-focus-ring]` ancestor, focused
// against unfocused; a component that draws its ring this way must set it.
export const FOCUS_RING = 'outline outline-2 outline-offset-2 outline-transparent focus-visible:outline-pick'
