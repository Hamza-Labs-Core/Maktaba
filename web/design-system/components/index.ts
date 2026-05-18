// Public surface of @maktaba/design-system. Story 17.2 inventory:
// Button (5×3), IconButton, Link, Input, Textarea, Select, Combobox,
// Toggle, Checkbox, Radio, Card, Modal, Drawer, Toast, Tooltip, Tabs,
// Pagination, ProgressBar, Skeleton, EmptyState, ErrorState, Avatar,
// Badge, Chip, Menu, ContextMenu.
//
// Shipped in HLB-303 gap-closure Wave 1: see README in the gap-analysis
// dir for the deferred set (Form/react-hook-form wrapper, Combobox,
// native TV parity).

export { Button } from "./Button/Button";
export type { ButtonProps } from "./Button/Button";

export { IconButton } from "./IconButton/IconButton";
export type { IconButtonProps } from "./IconButton/IconButton";

export { Link } from "./Link/Link";
export type { LinkProps } from "./Link/Link";

export { ThemeProvider, useTheme } from "./ThemeProvider/ThemeProvider";
export type { Theme } from "./ThemeProvider/ThemeProvider";

// --- Form primitives ---
export { FieldShell, fieldAria } from "./Field/Field";
export type { FieldShellProps } from "./Field/Field";

export { Input } from "./Input/Input";
export type { InputProps } from "./Input/Input";

export { Textarea } from "./Textarea/Textarea";
export type { TextareaProps } from "./Textarea/Textarea";

export { Select } from "./Select/Select";
export type { SelectProps, SelectOption } from "./Select/Select";

export { Checkbox } from "./Choice/Checkbox";
export type { CheckboxProps } from "./Choice/Checkbox";

export { Radio, RadioGroup } from "./Choice/Radio";
export type { RadioProps, RadioGroupProps } from "./Choice/Radio";

export { Toggle } from "./Choice/Toggle";
export type { ToggleProps } from "./Choice/Toggle";

// --- Layout & containers ---
export { Card } from "./Card/Card";
export type { CardProps } from "./Card/Card";

export { Modal } from "./Modal/Modal";
export type { ModalProps } from "./Modal/Modal";

export { Drawer } from "./Drawer/Drawer";
export type { DrawerProps } from "./Drawer/Drawer";

export { Tabs } from "./Tabs/Tabs";
export type { TabsProps, TabItem } from "./Tabs/Tabs";

// --- Feedback & status ---
export { Toast, ToastProvider, useToast } from "./Toast/Toast";
export type { ToastProps, ToastOptions } from "./Toast/Toast";

export { Tooltip } from "./Tooltip/Tooltip";
export type { TooltipProps } from "./Tooltip/Tooltip";

export { ProgressBar } from "./ProgressBar/ProgressBar";
export type { ProgressBarProps } from "./ProgressBar/ProgressBar";

export { Skeleton } from "./Skeleton/Skeleton";
export type { SkeletonProps } from "./Skeleton/Skeleton";

export { EmptyState } from "./EmptyState/EmptyState";
export type { EmptyStateProps } from "./EmptyState/EmptyState";

export { ErrorState } from "./ErrorState/ErrorState";
export type { ErrorStateProps, ErrorKind } from "./ErrorState/ErrorState";

// --- Data display ---
export { Avatar } from "./Avatar/Avatar";
export type { AvatarProps } from "./Avatar/Avatar";

export { Badge } from "./Badge/Badge";
export type { BadgeProps } from "./Badge/Badge";

export { Chip } from "./Chip/Chip";
export type { ChipProps } from "./Chip/Chip";

export { Pagination } from "./Pagination/Pagination";
export type { PaginationProps } from "./Pagination/Pagination";

// --- Overlays / navigation ---
export { Menu } from "./Menu/Menu";
export type { MenuProps, MenuItem } from "./Menu/Menu";

export { ContextMenu } from "./Menu/ContextMenu";
export type { ContextMenuProps } from "./Menu/ContextMenu";
