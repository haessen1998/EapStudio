import * as React from "react"
import { Send } from "lucide-react"
import { Button } from "@/components/ui/button"
import { cn } from "@/lib/utils"

export function PromptInput({ className, onSubmit, ...props }: React.FormHTMLAttributes<HTMLFormElement>) {
  return <form className={cn("flex items-end gap-2 rounded-xl border border-border bg-background p-2", className)} onSubmit={onSubmit} {...props} />
}
export function PromptInputTextarea(props: React.TextareaHTMLAttributes<HTMLTextAreaElement>) {
  return <textarea rows={2} className="max-h-28 min-h-10 flex-1 resize-none bg-transparent px-2 py-1.5 text-sm outline-none placeholder:text-muted-foreground" {...props} />
}
export function PromptInputSubmit({ loading, ...props }: React.ButtonHTMLAttributes<HTMLButtonElement> & { loading?: boolean }) {
  return <Button type="submit" size="icon" disabled={loading || props.disabled} {...props}>{loading ? <span className="size-4 animate-spin rounded-full border-2 border-current border-t-transparent" /> : <Send className="size-4" />}</Button>
}
