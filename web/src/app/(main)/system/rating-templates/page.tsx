"use client";

import * as React from "react";

import {
  ArrowDownIcon,
  ArrowUpIcon,
  DownloadIcon,
  PencilIcon,
  PlusIcon,
  ScaleIcon,
  Trash2Icon,
  UploadIcon,
} from "lucide-react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { NativeSelect, NativeSelectOption } from "@/components/ui/native-select";
import { Textarea } from "@/components/ui/textarea";
import { api } from "@/lib/api";
import type { ConstraintKind, RatingTemplate, RatingTemplateInput } from "@/lib/types";

type ClauseDraft = { code: string; kind: ConstraintKind; text: string };

const emptyClause = (): ClauseDraft => ({ code: "", kind: "allow", text: "" });

export default function RatingTemplatesPage() {
  const [templates, setTemplates] = React.useState<RatingTemplate[]>([]);
  const [loading, setLoading] = React.useState(true);
  const [open, setOpen] = React.useState(false);
  const [editing, setEditing] = React.useState<RatingTemplate | null>(null);
  const [name, setName] = React.useState("");
  const [description, setDescription] = React.useState("");
  const [clauses, setClauses] = React.useState<ClauseDraft[]>([emptyClause()]);
  const [saving, setSaving] = React.useState(false);
  const importInputRef = React.useRef<HTMLInputElement>(null);

  const load = React.useCallback(async () => {
    setLoading(true);
    try {
      setTemplates(await api.ratingTemplates());
    } catch (e) {
      toast.error("加载评级模板失败：" + (e as Error).message);
    } finally {
      setLoading(false);
    }
  }, []);

  React.useEffect(() => {
    void load();
  }, [load]);

  function openCreate() {
    setEditing(null);
    setName("");
    setDescription("");
    setClauses([emptyClause()]);
    setOpen(true);
  }

  function openEdit(t: RatingTemplate) {
    setEditing(t);
    setName(t.name);
    setDescription(t.description ?? "");
    setClauses(
      (t.clauses ?? []).map((c) => ({
        code: c.code,
        kind: c.kind,
        text: c.text,
      })),
    );
    setOpen(true);
  }

  // 导出为 JSON 文件（与 POST /api/rating-templates 的 body 同构），
  // 方便在另一台 ARTEX 上通过「导入模板」选择该 JSON 或直接调 API 复用。
  function exportTemplate(t: RatingTemplate) {
    const payload: RatingTemplateInput = {
      name: t.name,
      description: t.description ?? "",
      clauses: (t.clauses ?? []).map((c, i) => ({
        code: c.code,
        kind: c.kind,
        text: c.text,
        sort_order: i,
      })),
    };
    const blob = new Blob([JSON.stringify(payload, null, 2)], { type: "application/json;charset=utf-8" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = `${t.name.replace(/[\\/:*?"<>|]/g, "_")}-评级模板.json`;
    document.body.appendChild(a);
    a.click();
    a.remove();
    URL.revokeObjectURL(url);
  }

  async function importTemplate(file: File) {
    try {
      const raw = JSON.parse(await file.text()) as {
        name?: unknown;
        description?: unknown;
        clauses?: Array<{ code?: unknown; kind?: unknown; text?: unknown }>;
      };
      const input: RatingTemplateInput = {
        name: String(raw.name ?? "").trim(),
        description: String(raw.description ?? "").trim(),
        clauses: (Array.isArray(raw.clauses) ? raw.clauses : [])
          .map((c, i) => ({
            code: String(c.code ?? "").trim(),
            kind: c.kind === "deny" ? ("deny" as const) : ("allow" as const),
            text: String(c.text ?? "").trim(),
            sort_order: i,
          }))
          .filter((c) => c.text !== ""),
      };
      if (!input.name) throw new Error("文件缺少 name");
      if (input.clauses.length === 0) throw new Error("文件没有有效条款");
      await api.createRatingTemplate(input);
      toast.success(`已导入模板「${input.name}」`);
      await load();
    } catch (e) {
      toast.error("导入失败：" + (e as Error).message);
    } finally {
      if (importInputRef.current) importInputRef.current.value = "";
    }
  }

  function updateClause(index: number, patch: Partial<ClauseDraft>) {
    setClauses((prev) => prev.map((c, i) => (i === index ? { ...c, ...patch } : c)));
  }

  function moveClause(index: number, delta: number) {
    setClauses((prev) => {
      const next = [...prev];
      const target = index + delta;
      if (target < 0 || target >= next.length) return prev;
      [next[index], next[target]] = [next[target], next[index]];
      return next;
    });
  }

  async function save() {
    const cleaned = clauses
      .map((c) => ({ code: c.code.trim(), kind: c.kind, text: c.text.trim() }))
      .filter((c) => c.text !== "");
    if (!name.trim()) {
      toast.error("请填写模板名称");
      return;
    }
    if (cleaned.length === 0) {
      toast.error("至少需要一条条款");
      return;
    }
    const input: RatingTemplateInput = {
      name: name.trim(),
      description: description.trim(),
      clauses: cleaned.map((c, i) => ({ ...c, sort_order: i })),
    };
    setSaving(true);
    try {
      if (editing) {
        await api.updateRatingTemplate(editing.id, input);
        toast.success("模板已更新");
      } else {
        await api.createRatingTemplate(input);
        toast.success("模板已创建");
      }
      setOpen(false);
      await load();
    } catch (e) {
      toast.error("保存失败：" + (e as Error).message);
    } finally {
      setSaving(false);
    }
  }

  async function remove(t: RatingTemplate) {
    if (!window.confirm(`删除评级模板「${t.name}」？已建任务里的定级口径快照不受影响。`)) return;
    try {
      await api.deleteRatingTemplate(t.id);
      toast.success("模板已删除");
      await load();
    } catch (e) {
      toast.error("删除失败：" + (e as Error).message);
    }
  }

  return (
    <div className="flex flex-col gap-4 p-4">
      <div className="flex flex-wrap items-center gap-2">
        <div>
          <h1 className="flex items-center gap-2 font-semibold text-lg">
            <ScaleIcon className="size-5 text-primary" /> 漏洞评级模板
          </h1>
          <p className="text-muted-foreground text-sm">
            可复用的定级口径。任务创建或任务详情「应用模板」时会把条款快照进任务的定级口径；之后编辑模板不会影响已建任务。
          </p>
        </div>
        <div className="ml-auto flex items-center gap-2">
          <input
            ref={importInputRef}
            type="file"
            accept=".json,application/json"
            className="hidden"
            onChange={(e) => {
              const file = e.target.files?.[0];
              if (file) void importTemplate(file);
            }}
          />
          <Button size="sm" variant="outline" onClick={() => importInputRef.current?.click()}>
            <UploadIcon /> 导入模板
          </Button>
          <Button size="sm" onClick={openCreate}>
            <PlusIcon /> 新建模板
          </Button>
        </div>
      </div>

      {loading ? (
        <p className="text-muted-foreground text-sm">加载中…</p>
      ) : templates.length === 0 ? (
        <Card>
          <CardContent className="py-8 text-center text-muted-foreground text-sm">
            暂无评级模板，点击「新建模板」创建。
          </CardContent>
        </Card>
      ) : (
        <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
          {templates.map((t) => (
            <Card key={t.id} className="flex flex-col">
              <CardHeader>
                <CardTitle className="flex flex-wrap items-center gap-2 text-base">
                  {t.name}
                  {t.builtin && <Badge variant="secondary">内置</Badge>}
                  <span className="text-muted-foreground text-xs font-normal">{t.clauses?.length ?? 0} 条</span>
                </CardTitle>
                <CardDescription>{t.description || "—"}</CardDescription>
              </CardHeader>
              <CardContent className="flex-1">
                <div className="flex max-h-40 flex-col gap-1 overflow-y-auto text-sm">
                  {(t.clauses ?? []).map((c) => (
                    <div key={c.id} className="flex gap-2">
                      <Badge variant={c.kind === "allow" ? "default" : "outline"} className="shrink-0">
                        {c.code || (c.kind === "allow" ? "计入" : "不收取")}
                      </Badge>
                      <span className="text-muted-foreground line-clamp-2">{c.text}</span>
                    </div>
                  ))}
                </div>
              </CardContent>
              <CardContent className="flex flex-wrap gap-2 border-t pt-3">
                <Button size="sm" variant="outline" onClick={() => openEdit(t)}>
                  <PencilIcon /> 编辑
                </Button>
                <Button size="sm" variant="outline" onClick={() => exportTemplate(t)}>
                  <DownloadIcon /> 导出
                </Button>
                <Button size="sm" variant="ghost" className="text-destructive" onClick={() => void remove(t)}>
                  <Trash2Icon /> 删除
                </Button>
              </CardContent>
            </Card>
          ))}
        </div>
      )}

      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="max-w-[min(1500px,calc(100vw-2rem))] gap-0 overflow-hidden p-0 sm:max-w-[min(1500px,calc(100vw-2rem))]">
          <div className="flex max-h-[85dvh] flex-col">
            <DialogHeader className="border-b p-6">
              <DialogTitle>{editing ? "编辑评级模板" : "新建评级模板"}</DialogTitle>
              <DialogDescription>
                条款按顺序注入任务的定级口径；kind=计入 表示可计入目标，kind=不收取 表示忽略/不纳入。
              </DialogDescription>
            </DialogHeader>
            <div className="flex-1 space-y-4 overflow-y-auto p-6">
              <div className="grid gap-2">
                <Label htmlFor="rating-template-name">名称</Label>
                <Input
                  id="rating-template-name"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  placeholder="例如：互联网 SRC / 银行 SRC / 攻防"
                />
              </div>
              <div className="grid gap-2">
                <Label htmlFor="rating-template-desc">说明（可选）</Label>
                <Textarea
                  id="rating-template-desc"
                  rows={1}
                  value={description}
                  onChange={(e) => setDescription(e.target.value)}
                  placeholder="这套模板适用的场景、来源或版本说明"
                  className="field-sizing-fixed min-h-9 max-h-40 resize-y px-2.5 py-1.5"
                />
              </div>
              <div className="flex flex-col gap-2">
                <div className="flex items-center justify-between">
                  <Label>条款</Label>
                  <Button size="sm" variant="outline" onClick={() => setClauses((prev) => [...prev, emptyClause()])}>
                    <PlusIcon /> 添加条款
                  </Button>
                </div>
                <div className="flex flex-col overflow-hidden rounded-md border">
                  {clauses.map((c, i) => (
                    <div key={i} className="flex flex-wrap items-center gap-1 border-b bg-muted/20 p-1 last:border-b-0">
                      <span className="text-muted-foreground w-5 shrink-0 text-xs tabular-nums">#{i + 1}</span>
                      <Input
                        className="w-24"
                        value={c.code}
                        onChange={(e) => updateClause(i, { code: e.target.value })}
                        placeholder="严重-1"
                      />
                      <NativeSelect
                        className="w-20"
                        value={c.kind}
                        onChange={(e) => updateClause(i, { kind: e.target.value as ConstraintKind })}
                      >
                        <NativeSelectOption value="allow">计入</NativeSelectOption>
                        <NativeSelectOption value="deny">不收取</NativeSelectOption>
                      </NativeSelect>
                      <Textarea
                        rows={1}
                        className="field-sizing-fixed min-h-10 max-h-40 min-w-64 flex-1 resize-y px-2.5 py-1"
                        value={c.text}
                        title={c.text}
                        onChange={(e) => updateClause(i, { text: e.target.value })}
                        placeholder="条款内容，例如：直接获取服务器/系统权限（RCE、命令执行、提权…）"
                      />
                      <div className="ml-auto flex items-center gap-0.5">
                        <Button
                          size="icon-sm"
                          variant="ghost"
                          disabled={i === 0}
                          onClick={() => moveClause(i, -1)}
                          title="上移"
                        >
                          <ArrowUpIcon className="size-3.5" />
                        </Button>
                        <Button
                          size="icon-sm"
                          variant="ghost"
                          disabled={i === clauses.length - 1}
                          onClick={() => moveClause(i, 1)}
                          title="下移"
                        >
                          <ArrowDownIcon className="size-3.5" />
                        </Button>
                        <Button
                          size="icon-sm"
                          variant="ghost"
                          className="text-destructive"
                          onClick={() => setClauses((prev) => prev.filter((_, j) => j !== i))}
                          title="删除条款"
                        >
                          <Trash2Icon className="size-3.5" />
                        </Button>
                      </div>
                    </div>
                  ))}
                </div>
              </div>
            </div>
            <DialogFooter className="flex-row justify-end gap-2 border-t p-4">
              <Button variant="outline" onClick={() => setOpen(false)}>
                取消
              </Button>
              <Button onClick={() => void save()} disabled={saving}>
                {saving ? "保存中…" : "保存"}
              </Button>
            </DialogFooter>
          </div>
        </DialogContent>
      </Dialog>
    </div>
  );
}
