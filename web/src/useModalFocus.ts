import { useEffect, useRef, type KeyboardEvent } from 'react'

const focusable = 'button:not(:disabled), a[href], input:not(:disabled):not([type="hidden"]), select:not(:disabled), textarea:not(:disabled), [tabindex]:not([tabindex="-1"])'

function available(element: HTMLElement, panel: HTMLElement) {
  if (element.matches(':disabled') || element.closest('[hidden], [inert]')) return false
  for (let ancestor: HTMLElement | null = element; ancestor && ancestor !== panel.parentElement; ancestor = ancestor.parentElement) {
    const style = getComputedStyle(ancestor)
    if (style.display === 'none' || style.visibility === 'hidden' || style.visibility === 'collapse') return false
  }
  return true
}

export function useModalFocus(onClose: () => void, busy: boolean) {
  const ref = useRef<HTMLElement>(null)
  useEffect(() => {
    const previous = document.activeElement instanceof HTMLElement ? document.activeElement : null
    const panel = ref.current
    const background = new Map<HTMLElement, string | null>()
    // Isolate every sibling subtree outside the dialog's ancestor path,
    // including controls mounted within the same React root.
    for (let branch = panel; branch?.parentElement && branch !== document.body; branch = branch.parentElement) {
      for (const sibling of branch.parentElement.children) {
        if (sibling !== branch && sibling instanceof HTMLElement) {
          background.set(sibling, sibling.getAttribute('inert'))
          sibling.setAttribute('inert', '')
        }
      }
    }
    const initial = panel && [...panel.querySelectorAll<HTMLElement>(focusable)].find(el => available(el, panel))
    ;(initial || panel)?.focus()
    return () => {
      for (const [element, inert] of background) {
        if (inert === null) element.removeAttribute('inert')
        else element.setAttribute('inert', inert)
      }
      if (previous?.isConnected) previous.focus()
    }
  }, [])
  function onKeyDown(event: KeyboardEvent<HTMLElement>) {
    if (event.key === 'Escape') {
      event.preventDefault()
      event.stopPropagation()
      if (!busy) onClose()
    } else if (event.key === 'Tab') {
      const panel = ref.current
      const controls = panel ? [...panel.querySelectorAll<HTMLElement>(focusable)].filter(el => available(el, panel)) : []
      const first = controls[0], last = controls.at(-1)
      if (!first) { event.preventDefault(); panel?.focus() }
      else if (event.shiftKey && (document.activeElement === first || document.activeElement === panel)) { event.preventDefault(); last?.focus() }
      else if (!event.shiftKey && (document.activeElement === last || document.activeElement === panel)) { event.preventDefault(); first.focus() }
    }
  }
  // Disabling the focused confirm button otherwise leaves focus outside the
  // enabled controls; keep Escape/Tab routed through the panel while executing.
  useEffect(() => { if (busy) ref.current?.focus() }, [busy])
  return { ref, onKeyDown, tabIndex: -1 }
}
