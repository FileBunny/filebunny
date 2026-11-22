// After a form is done, the page shows its result in place of the form.
// The window keeps its scroll position, so on a phone the result (with
// the share link at the top) could start above the screen. This moves
// to the top and puts the focus on the result's heading, which also
// tells a screen reader that the page changed.

/** Shows the page's first heading: scrolled to the top, focused. Call after the result is rendered. */
export function revealResult(): void {
  const h = document.querySelector<HTMLElement>('main h1');
  if (h) {
    h.tabIndex = -1;
    h.focus({ preventScroll: true });
  }
  window.scrollTo(0, 0);
}
