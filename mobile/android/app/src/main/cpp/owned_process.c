// Each handle owns one unreaped child/process-group leader. Keeping the leader
// waitable until cleanup prevents a recycled PID from becoming a signal target.
#ifndef _GNU_SOURCE
#define _GNU_SOURCE
#endif
#include <jni.h>
#include <errno.h>
#include <fcntl.h>
#include <poll.h>
#include <pthread.h>
#include <signal.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <sys/ioctl.h>
#include <sys/types.h>
#include <sys/wait.h>
#include <termios.h>
#include <unistd.h>

#define JNI_FN(name) Java_dev_agentdock_workbench_shizuku_NativeCommand_##name
#define RUNNING (-1000)
struct owned { pid_t pid; int input, output, error, tty; };

static void fail(JNIEnv *env, const char *message) {
    jclass cls = (*env)->FindClass(env, "java/io/IOException");
    if (cls != NULL) (*env)->ThrowNew(env, cls, message);
}
static void close_fd(int *fd) { if (*fd >= 0) { close(*fd); *fd = -1; } }
static void nonblock(int fd) {
    int flags = fcntl(fd, F_GETFL, 0);
    if (flags >= 0) (void)fcntl(fd, F_SETFL, flags | O_NONBLOCK);
}
static char *utf8(JNIEnv *env, jbyteArray value, int maximum) {
    if (value == NULL) return NULL;
    jsize size = (*env)->GetArrayLength(env, value);
    if (size <= 0 || size > maximum) return NULL;
    char *result = malloc((size_t)size + 1);
    if (!result) return NULL;
    (*env)->GetByteArrayRegion(env, value, 0, size, (jbyte *)result);
    if ((*env)->ExceptionCheck(env) || memchr(result, 0, (size_t)size)) { free(result); return NULL; }
    result[size] = 0;
    return result;
}
static struct owned *get(JNIEnv *env, jlong handle) {
    struct owned *p = (struct owned *)(intptr_t)handle;
    if (!p || p->pid <= 1) { fail(env, "Invalid owned process handle"); return NULL; }
    return p;
}
static int child_state(pid_t pid, int *status) {
    siginfo_t info;
    memset(&info, 0, sizeof(info));
    int rc;
    do { rc = waitid(P_PID, (id_t)pid, &info, WEXITED | WNOHANG | WNOWAIT); } while (rc < 0 && errno == EINTR);
    if (rc < 0) return -1;
    if (info.si_pid == 0) return 0;
    *status = info.si_code == CLD_EXITED ? info.si_status : 128 + info.si_status;
    return 1;
}

JNIEXPORT jlong JNICALL JNI_FN(spawn)(JNIEnv *env, jclass klass, jbyteArray cmd, jbyteArray cwd, jobjectArray entries, jboolean tty) {
    (void)klass;
    char *command = utf8(env, cmd, 16384), *directory = utf8(env, cwd, 4096);
    char **environment = NULL;
    struct owned *p = NULL;
    int in[2] = {-1,-1}, out[2] = {-1,-1}, err[2] = {-1,-1}, startup[2] = {-1,-1};
    int master = -1, slave = -1, count = entries == NULL ? 0 : (*env)->GetArrayLength(env, entries);
    pid_t pid = -1;
    if (!command || !directory || directory[0] != '/' || count < 0 || count > 80) goto failure;
    environment = calloc((size_t)count + 1, sizeof(char *));
    if (!environment) goto failure;
    size_t total = 0;
    for (int i = 0; i < count; ++i) {
        jbyteArray entry = (jbyteArray)(*env)->GetObjectArrayElement(env, entries, i);
        environment[i] = utf8(env, entry, 16384);
        if (entry != NULL) (*env)->DeleteLocalRef(env, entry);
        if (!environment[i] || !strchr(environment[i], '=')) goto failure;
        total += strlen(environment[i]);
        if (total > 32768) goto failure;
    }
    p = calloc(1, sizeof(*p));
    if (!p) goto failure;
    p->input = p->output = p->error = -1;
    if (pipe2(startup, O_CLOEXEC) < 0) goto failure;
    if (tty) {
        master = posix_openpt(O_RDWR | O_NOCTTY | O_CLOEXEC);
        char name[256];
        if (master < 0 || grantpt(master) || unlockpt(master) || ptsname_r(master, name, sizeof(name))) goto failure;
        slave = open(name, O_RDWR | O_NOCTTY | O_CLOEXEC);
        if (slave < 0) goto failure;
        struct winsize size = {.ws_row = 40, .ws_col = 160};
        (void)ioctl(slave, TIOCSWINSZ, &size);
    } else if (pipe2(in, O_CLOEXEC) || pipe2(out, O_CLOEXEC) || pipe2(err, O_CLOEXEC)) goto failure;
    // Allocate argv and all data before fork. The child makes no JNI, allocation,
    // logging or environment-library calls between fork and execve.
    char *argv[] = {"/system/bin/sh", "-c", command, NULL};
    pid = fork();
    if (pid < 0) goto failure;
    if (pid == 0) {
        close(startup[0]);
        int failure_code = 0;
        if (setsid() < 0) failure_code = errno;
        if (!failure_code && tty && ioctl(slave, TIOCSCTTY, 0) < 0) failure_code = errno;
        if (!failure_code && (dup2(tty ? slave : in[0], STDIN_FILENO) < 0 ||
            dup2(tty ? slave : out[1], STDOUT_FILENO) < 0 || dup2(tty ? slave : err[1], STDERR_FILENO) < 0)) failure_code = errno;
        if (!failure_code && chdir(directory) < 0) failure_code = errno;
        if (!failure_code) { execve(argv[0], argv, environment); failure_code = errno; }
        (void)write(startup[1], &failure_code, sizeof(failure_code));
        _exit(127);
    }
    close_fd(&startup[1]);
    // EOF means exec succeeded. A startup error never yields a running handle.
    struct pollfd ready = {.fd = startup[0], .events = POLLIN | POLLHUP};
    int polled;
    do { polled = poll(&ready, 1, 2000); } while (polled < 0 && errno == EINTR);
    int child_error = 0;
    ssize_t received = polled > 0 ? read(startup[0], &child_error, sizeof(child_error)) : -1;
    if (received != 0) goto failure;
    close_fd(&startup[0]);
    p->pid = pid; p->tty = tty;
    if (tty) {
        close_fd(&slave);
        p->input = fcntl(master, F_DUPFD_CLOEXEC, 3);
        if (p->input < 0) goto failure;
        p->output = master; master = -1;
    } else {
        close_fd(&in[0]); close_fd(&out[1]); close_fd(&err[1]);
        p->input = in[1]; in[1] = -1;
        p->output = out[0]; out[0] = -1;
        p->error = err[0]; err[0] = -1;
    }
    nonblock(p->input); nonblock(p->output); if (p->error >= 0) nonblock(p->error);
    for (int i=0; i<count; ++i) free(environment[i]);
    free(environment); free(command); free(directory);
    return (jlong)(intptr_t)p;

failure:
    if (pid > 1) {
        // Still our unreaped direct child, even if startup failed before setsid.
        (void)kill(-pid, SIGKILL); (void)kill(pid, SIGKILL);
        while (waitpid(pid, NULL, 0) < 0 && errno == EINTR) {}
    }
    for (int i=0; i<2; ++i) { close_fd(&in[i]); close_fd(&out[i]); close_fd(&err[i]); close_fd(&startup[i]); }
    close_fd(&master); close_fd(&slave);
    if (p) { close_fd(&p->input); close_fd(&p->output); close_fd(&p->error); free(p); }
    if (environment) { for (int i=0; i<count; ++i) free(environment[i]); free(environment); }
    free(command); free(directory);
    if (!(*env)->ExceptionCheck(env)) fail(env, "Owned Android process could not start");
    return 0;
}
JNIEXPORT jbyteArray JNICALL JNI_FN(read)(JNIEnv *env, jclass klass, jlong handle, jint stream) {
    (void)klass;
    struct owned *p = get(env, handle); if (!p) return NULL;
    if (stream != 1 && stream != 2) { fail(env, "Invalid stream"); return NULL; }
    int *fd = stream == 1 ? &p->output : &p->error;
    if (*fd < 0) return NULL;
    jbyte buffer[16384]; ssize_t n;
    do { n = read(*fd, buffer, sizeof(buffer)); } while (n < 0 && errno == EINTR);
    if (n == 0 || (n < 0 && errno == EIO && p->tty)) { close_fd(fd); return NULL; }
    if (n < 0 && (errno == EAGAIN || errno == EWOULDBLOCK)) n = 0;
    else if (n < 0) { fail(env, "Owned process output read failed"); return NULL; }
    jbyteArray value = (*env)->NewByteArray(env, (jsize)n);
    if (value && n) (*env)->SetByteArrayRegion(env, value, 0, (jsize)n, buffer);
    return value;
}
JNIEXPORT jint JNICALL JNI_FN(write)(JNIEnv *env, jclass klass, jlong handle, jbyteArray bytes, jint offset) {
    (void)klass;
    struct owned *p = get(env, handle); if (!p) return -1;
    if (bytes == NULL || p->input < 0) { fail(env, "Owned stdin is closed"); return -1; }
    int size = (*env)->GetArrayLength(env, bytes);
    if (offset < 0 || offset > size || size > 16384) { fail(env, "Invalid input bounds"); return -1; }
    if (offset == size) return 0;
    jbyte buffer[16384]; (*env)->GetByteArrayRegion(env, bytes, offset, size-offset, buffer);
    if ((*env)->ExceptionCheck(env)) return -1;
    sigset_t blocked, previous;
    sigemptyset(&blocked); sigaddset(&blocked, SIGPIPE);
    if (pthread_sigmask(SIG_BLOCK, &blocked, &previous) != 0) { fail(env, "Cannot guard owned stdin signal"); return -1; }
    ssize_t n;
    do { n = write(p->input, buffer, (size_t)(size-offset)); } while (n < 0 && errno == EINTR);
    int saved_errno = errno;
    if (n < 0 && saved_errno == EPIPE && !sigismember(&previous, SIGPIPE)) {
        struct timespec now = {0, 0};
        (void)sigtimedwait(&blocked, NULL, &now);
    }
    (void)pthread_sigmask(SIG_SETMASK, &previous, NULL);
    if (n < 0 && (saved_errno == EAGAIN || saved_errno == EWOULDBLOCK)) return 0;
    if (n < 0) { fail(env, "Owned process stdin write failed"); return -1; }
    return (jint)n;
}
JNIEXPORT void JNICALL JNI_FN(closeInput)(JNIEnv *env, jclass klass, jlong handle) {
    (void)klass; struct owned *p = get(env, handle); if (p) close_fd(&p->input);
}
JNIEXPORT jint JNICALL JNI_FN(poll)(JNIEnv *env, jclass klass, jlong handle) {
    (void)klass; struct owned *p = get(env, handle); if (!p) return -1;
    int status = 0, state = child_state(p->pid, &status);
    if (state < 0) { fail(env, "Owned child identity no longer waitable"); return -1; }
    return state == 0 ? RUNNING : status;
}
JNIEXPORT void JNICALL JNI_FN(terminate)(JNIEnv *env, jclass klass, jlong handle) {
    (void)klass; struct owned *p = get(env, handle); if (!p) return;
    int code = 0;
    if (child_state(p->pid, &code) < 0) { fail(env, "Refusing to signal a child without ownership"); return; }
    if (kill(-p->pid, SIGKILL) < 0 && errno != ESRCH) fail(env, "Owned group termination failed");
}
JNIEXPORT void JNICALL JNI_FN(release)(JNIEnv *env, jclass klass, jlong handle) {
    (void)klass; struct owned *p = get(env, handle); if (!p) return;
    int code = 0;
    if (child_state(p->pid, &code) != 1) { fail(env, "Cannot release a running or unowned child"); return; }
    // Group cleanup happens before waitpid can release the leader's PID.
    (void)kill(-p->pid, SIGKILL);
    while (waitpid(p->pid, NULL, 0) < 0 && errno == EINTR) {}
    p->pid = 0;
    close_fd(&p->input); close_fd(&p->output); close_fd(&p->error); free(p);
}
