/* Evaluate as `(<this source>)(offset)` only after inspecting the Notion page.
 * Scrolls the document viewport. It never expands toggles or clicks controls.
 */
(function scrollNotion(offset) {
  if (!Number.isFinite(offset) || offset < 0 || offset > 40000) throw new Error('Invalid document offset.');
  const scroller = document.querySelector('.notion-scroller.vertical');
  if (!scroller || !document.querySelector('.notion-page-content')) throw new Error('No supported Notion scroller.');
  scroller.scrollTo({ top: offset, behavior: 'instant' });
  return { offset: scroller.scrollTop };
})
