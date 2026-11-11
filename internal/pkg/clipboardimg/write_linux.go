//go:build linux && cgo && (dev || production)

package clipboardimg

/*
#cgo pkg-config: gtk+-3.0
#include <gtk/gtk.h>
#include <stdlib.h>
#include <string.h>

// GTK 只能在主线程上动剪贴板；Wails 的绑定调用跑在别的 goroutine 上，所以把写入
// 投到主循环（g_idle_add），再在调用线程上等它做完、把结果带回来。
typedef struct {
	const guchar *data;
	gsize len;
	gboolean done;
	gboolean ok;
	char err[256];
	GMutex mu;
	GCond cond;
} agentreClipJob;

static void agentreFinish(agentreClipJob *job, gboolean ok, const char *msg) {
	g_mutex_lock(&job->mu);
	job->ok = ok;
	if (msg != NULL) {
		g_strlcpy(job->err, msg, sizeof(job->err));
	}
	job->done = TRUE;
	g_cond_signal(&job->cond);
	g_mutex_unlock(&job->mu);
}

static gboolean agentreSetImage(gpointer data) {
	agentreClipJob *job = data;
	GError *error = NULL;
	GdkPixbufLoader *loader = gdk_pixbuf_loader_new_with_type("png", &error);
	if (loader == NULL) {
		agentreFinish(job, FALSE, error != NULL ? error->message : "gdk_pixbuf_loader_new_with_type failed");
		g_clear_error(&error);
		return G_SOURCE_REMOVE;
	}
	// close 写失败时也要调：没 close 就 unref，GdkPixbufLoader 会在 finalize 时报警告。
	// 写失败时 close 的错误丢掉，留下写那一步的原因。
	gboolean wrote = gdk_pixbuf_loader_write(loader, job->data, job->len, &error);
	gboolean closed = gdk_pixbuf_loader_close(loader, wrote ? &error : NULL);
	if (!wrote || !closed) {
		agentreFinish(job, FALSE, error != NULL ? error->message : "gdk_pixbuf_loader failed");
		g_clear_error(&error);
		g_object_unref(loader);
		return G_SOURCE_REMOVE;
	}
	GdkPixbuf *pixbuf = gdk_pixbuf_loader_get_pixbuf(loader);
	if (pixbuf == NULL) {
		agentreFinish(job, FALSE, "gdk_pixbuf_loader produced no image");
		g_object_unref(loader);
		return G_SOURCE_REMOVE;
	}
	gtk_clipboard_set_image(gtk_clipboard_get(GDK_SELECTION_CLIPBOARD), pixbuf);
	g_object_unref(loader);
	agentreFinish(job, TRUE, NULL);
	return G_SOURCE_REMOVE;
}

// 返回 1 表示成功；失败时把原因写进 errbuf。
static int agentreWritePNG(const guchar *bytes, gsize length, char *errbuf, gsize errlen) {
	agentreClipJob job;
	memset(&job, 0, sizeof(job));
	job.data = bytes;
	job.len = length;
	g_mutex_init(&job.mu);
	g_cond_init(&job.cond);

	g_idle_add(agentreSetImage, &job);

	g_mutex_lock(&job.mu);
	while (!job.done) {
		g_cond_wait(&job.cond, &job.mu);
	}
	g_mutex_unlock(&job.mu);

	g_mutex_clear(&job.mu);
	g_cond_clear(&job.cond);
	if (!job.ok) {
		g_strlcpy(errbuf, job.err, errlen);
		return 0;
	}
	return 1;
}
*/
import "C"

import "fmt"

func writePNG(png []byte) error {
	if len(png) == 0 {
		return fmt.Errorf("clipboardimg: empty image")
	}
	data := C.CBytes(png)
	defer C.free(data)

	var errbuf [256]C.char
	if C.agentreWritePNG((*C.guchar)(data), C.gsize(len(png)), &errbuf[0], C.gsize(len(errbuf))) == 0 {
		return fmt.Errorf("clipboardimg: GTK clipboard: %s", C.GoString(&errbuf[0]))
	}
	return nil
}
