package com.yunx.app.data.network.video

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.coroutines.sync.Semaphore
import kotlinx.coroutines.sync.withPermit
import org.json.JSONObject
import java.util.concurrent.TimeUnit

/** NAS Chromium adapter; no desktop window or JCEF native dependency. */
object BrowserCookies { val current = ThreadLocal<String>() }
object HeadlessWebView {
    private val browsers = Semaphore(1)
    suspend fun loadAndExtract(url: String, userAgent: String, readyCheckJs: String, extractJs: String,
        timeoutMs: Long = 30000, extraDocumentStartJs: List<String> = emptyList()): String? = browsers.withPermit { withContext(Dispatchers.IO) {
        val script = System.getenv("YUNX_BROWSER_SCRIPT") ?: throw IllegalStateException("Browser worker path missing")
        val p = ProcessBuilder("node", script).redirectError(ProcessBuilder.Redirect.DISCARD).start()
        val watchdog = java.util.Timer(true)
        watchdog.schedule(object: java.util.TimerTask(){override fun run(){p.descendants().forEach { it.destroyForcibly() };p.destroyForcibly()}}, timeoutMs.coerceAtMost(40000)+5000)
        try {
            val input = JSONObject().put("url", url).put("userAgent", userAgent).put("readyCheckJs", readyCheckJs)
                .put("extractJs", extractJs).put("timeout", timeoutMs.coerceAtMost(40000)).put("init", extraDocumentStartJs).put("cookie",BrowserCookies.current.get().orEmpty())
            p.outputStream.bufferedWriter().use { it.write(input.toString()); it.newLine() }
            // Worker output is bounded to 2 MiB and closes itself by its deadline.
            val result = p.inputStream.readNBytes(2 * 1024 * 1024 + 1)
            check(result.size <= 2 * 1024 * 1024) { "Browser response too large" }
            check(p.waitFor(3, TimeUnit.SECONDS) && p.exitValue() == 0) { "Browser extraction failed" }
            result.toString(Charsets.UTF_8).trim().takeIf { it.isNotEmpty() }
        } finally { watchdog.cancel();p.descendants().forEach { it.destroyForcibly() };p.destroyForcibly() }
    } }
}
