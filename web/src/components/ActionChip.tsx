import type { ComponentProps } from 'react';
import { Button, buttonVariants } from './ui/button';
import { Icon, type IconName } from './Icon';

// The same warm, quiet capsule used by the existing execution-step controls.
export const actionChipClass = buttonVariants({ variant: 'pill', size: 'compact' });
export function ActionChip({ icon, children, ...props }: ComponentProps<typeof Button> & { icon?: IconName }) {
  return <Button type="button" variant="pill" size="compact" {...props}>
    {icon && <Icon name={icon} size={13} />}{children}
  </Button>;
}
