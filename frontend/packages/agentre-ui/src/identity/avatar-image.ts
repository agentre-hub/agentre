// 上传头像只允许内联的栅格图片；存储或宿主传来的字符串不能变成网络请求。
export const avatarMimeTypes = ["image/png", "image/jpeg", "image/webp"];
export const maxAvatarBytes = 2 * 1024 * 1024;

export function uploadedAvatarSrc(value?: string): string | undefined {
  if (!value) return undefined;
  return avatarMimeTypes.some((mime) => {
    const prefix = `data:${mime};base64,`;
    const length = value.length - prefix.length;
    return (
      length > 0 &&
      length <= 4 * Math.ceil(maxAvatarBytes / 3) &&
      value.startsWith(prefix)
    );
  })
    ? value
    : undefined;
}
