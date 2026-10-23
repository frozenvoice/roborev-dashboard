import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'
import { useModalFocus } from './useModalFocus'

afterEach(cleanup)
function Fixture({ busy = false, close = () => {} }) {
  const modal = useModalFocus(close, busy)
  return <><button>Background</button><div><section {...modal} role="dialog"><button style={{ display: 'none' }}>Hidden</button><button disabled>Disabled</button><button>First</button><button>Last</button></section></div></>
}

it('isolates background ancestors, preserves existing inert and restores opener after cleanup', async () => {
  const opener = document.createElement('button'), existing = document.createElement('div')
  existing.setAttribute('inert', 'inert'); document.body.append(opener, existing); opener.focus()
  const view = render(<Fixture />)
  expect(opener).toHaveAttribute('inert')
  expect(screen.getByText('Background')).toHaveAttribute('inert')
  expect(screen.getByRole('button', { name: 'First' })).toHaveFocus()
  await userEvent.tab({ shift: true }); expect(screen.getByRole('button', { name: 'Last' })).toHaveFocus()
  await userEvent.tab(); expect(screen.getByRole('button', { name: 'First' })).toHaveFocus()
  view.unmount()
  expect(opener).not.toHaveAttribute('inert'); expect(existing).toHaveAttribute('inert', 'inert'); expect(opener).toHaveFocus()
  opener.remove(); existing.remove()
})

it('keeps focus contained and refuses Escape while execution is pending', async () => {
  const close = vi.fn()
  const view = render(<Fixture close={close} />)
  view.rerender(<Fixture busy close={close} />)
  expect(screen.getByRole('dialog')).toHaveFocus()
  await userEvent.keyboard('{Escape}'); expect(close).not.toHaveBeenCalled()
  await userEvent.tab(); expect(screen.getByRole('button', { name: 'First' })).toHaveFocus()
})
