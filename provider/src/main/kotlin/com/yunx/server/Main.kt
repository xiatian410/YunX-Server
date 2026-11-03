package com.yunx.server

import kotlinx.coroutines.*
import com.yunx.app.data.network.ProviderDeadline
import kotlinx.coroutines.sync.Semaphore
import kotlinx.coroutines.sync.withPermit
import org.json.JSONObject
import java.util.concurrent.Executors

/** Newline JSON over inherited pipes; no public listening socket, no credentials in arguments/logs. */
fun main() = runBlocking {
    val pool = Executors.newFixedThreadPool(4).asCoroutineDispatcher()
    val gate = Semaphore(4)
    val output = System.out
    try {
        val reader = System.`in`.bufferedReader()
        while (true) {
            val line = reader.readLine() ?: break
            if (line.length > 2 * 1024 * 1024) break
            val request = runCatching { JSONObject(line) }.getOrNull() ?: break
            gate.acquire()
            launch(pool) {
                val response = JSONObject().put("id", request.getString("id"))
                try { response.put("result", withTimeout(85000) { withContext(ProviderDeadline.current.asContextElement(System.nanoTime()+80_000_000_000L)) { Adapter(request).execute() } }) }
                catch (e: Exception) {
                    // Upstream exceptions can contain HTTP bodies and credentials; do not serialize them.
                    val code = when (e) {
                        is CancellationException -> "provider_timeout"
                        is java.net.UnknownHostException -> "blocked_address"
                        is com.yunx.app.data.network.video.VideoParseException -> "video_" + e.kind.name.lowercase()
                        else -> "provider_failed"
                    }
                    response.put("error", JSONObject().put("code", code).put("type", e.javaClass.simpleName))
                } finally { gate.release() }
                synchronized(output) { output.println(response); output.flush() }
            }
        }
    } finally { coroutineContext.job.children.toList().joinAll(); pool.close() }
}
