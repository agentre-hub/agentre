import { toast } from "sonner";

import { classifyAppError } from "@/lib/app-error";

import { FailureBody } from "../failure-body";

/**
 * 组织架构里「没有字段可贴」的写失败（拖拽、排序、删除…）统一这样说：标题是调用方
 * 给的本地化事实句，正文是后端带码的那句话，机器原话只进默认折叠的「详情」。
 */
export function toastOrgWriteFailure(title: string, err: unknown) {
  const { message, detail } = classifyAppError(err);
  toast.error(title, {
    description:
      message || detail ? (
        <FailureBody message={message} detail={detail} />
      ) : undefined,
  });
}
