package com.yunx.server

import com.yunx.app.data.network.*
import com.yunx.app.data.network.model.*
import com.yunx.app.data.network.video.*
import com.yunx.app.data.repository.*
import kotlinx.coroutines.withContext
import kotlinx.coroutines.asContextElement
import org.json.JSONObject
import org.json.JSONArray

private fun obj(vararg pairs: Pair<String, Any?>) = JSONObject().apply { pairs.forEach { put(it.first, it.second ?: JSONObject.NULL) } }
private fun ShareFile.json() = obj("fid" to fid,"fname" to fname,"fsize" to fsize,"isdir" to isdir,"pdirFid" to pdirFid,"fidToken" to fidToken)
private fun file(j: JSONObject) = ShareFile(j.getString("fid"),j.getString("fname"),j.optLong("fsize",-1),j.optBoolean("isdir"),j.optString("pdirFid"),j.optString("fidToken"))
private fun ShareFile.item() = obj("id" to fid,"name" to fname,"size" to fsize,"directory" to isdir,"extra" to obj("provider_file" to json().toString()))

class Adapter(private val r: JSONObject) {
    private val c = r.optJSONObject("credentials") ?: JSONObject()
    private val platform = r.optString("platform")
    private val cookie = c.optString("cookie")
    private val token = c.optString("token")
    private val credential get() = if (platform in setOf("pan123","xunlei","guangya","ilanzou")) token else cookie
    private val device = c.optString("device_id").ifBlank { Pan123DeviceId.value() }
    private val captcha = c.optString("captcha_token")
    private var cachedUuid: String? = null
    private suspend fun deviceUUID(): String {
        cachedUuid?.let { return it }
        val value=c.optString("uuid").ifBlank { check(token.isBlank()) { "Missing account UUID" };ilanzou.getUuid() }
        cachedUuid=value;return value
    }
    private val cloud = r.optBoolean("cloud")
    private val parent = r.optString("parent","0")
    private val page = r.optInt("page",1)
    private val quark = QuarkApi()
    private val uc = UCApi()
    private val pan123 = Pan123Api()
    private val baidu = BaiduApi()
    private val c139 = C139Api()
    private val pan115 = Pan115Api()
    private val guangya = GuangYaApi().also { it.deviceIdProvider = { device } }
    private val lanzou = LanzouApi()
    private val ilanzou = ILanzouApi()
    private val xunlei = XunleiApi()
    private val video get() = VideoPlatform.entries.find { it.name.lowercase() == platform }
    private fun repo(): ShareResolveRepository? = when(platform) {
        "quark" -> QuarkResolveRepository(quark)
        "uc" -> UCResolveRepository(uc)
        "pan123" -> Pan123ResolveRepository(pan123) { token }
        "baidu" -> BaiduResolveRepository(baidu)
        "c139" -> C139ResolveRepository(c139)
        "pan115" -> Pan115ResolveRepository(pan115)
        "guangya" -> GuangYaResolveRepository(guangya) { token }
        "lanzou" -> LanzouResolveRepository(lanzou)
        else -> null
    }
    private suspend fun share(repo: ShareResolveRepository): ShareSession =
        repo.createSession(r.getString("text"),r.optString("password").takeIf { it.isNotBlank() },credential).getOrThrow()
    private fun parsed() = ShareLinkParser.parse(r.getString("text")) ?: error("Unrecognized share link")
    private suspend fun videoResult(page: Int = 0): VideoParseResult {
        var link = VideoLinkParser.parse(r.getString("text")) ?: error("Unrecognized video link")
        if(page>0) {
            val url=link.url.replace(Regex("([?&])p=[0-9]+"),"$1").trimEnd('?','&')
            link=link.copy(url=url+(if(url.contains('?')) "&" else "?")+"p=$page")
        }
        val parser = VideoParsers.of(link.platform) ?: error("Missing video parser")
        return withContext(BrowserCookies.current.asContextElement(cookie)) { if (parser is QualitySelectableParser) parser.parseWithQuality(link,cookie,r.optInt("quality",0).takeIf { it > 0 }) else parser.parse(link,cookie) }
    }
    suspend fun execute(): JSONObject = when(r.getString("op")) {
        "health" -> obj("protocol" to 1,"platforms" to JSONArray(SharePlatform.entries.map { it.name.lowercase() } + VideoPlatform.entries.map { it.name.lowercase() }))
        "detect" -> {
            val share = ShareLinkParser.parse(r.getString("text"))
            val video = VideoLinkParser.parse(r.getString("text"))
            obj("platform" to (share?.platform?.name?.lowercase() ?: video?.platform?.name?.lowercase() ?: ""),"password" to share?.pwd)
        }
        "resolve" -> if (cloud) obj("title" to platform) else if (video != null) videoItems(videoResult()) else {
            val repo=repo()
            if(repo!=null) { val s=share(repo);obj("title" to s.title) }
            else obj("title" to parsed().shareId)
        }
        "list" -> if(video!=null) videoItems(videoResult()) else list()
        "sources" -> if(video!=null) videoSources() else sources()
        "check" -> if(platform in setOf("douyin","kuaishou","weibo","acfun")) obj("known" to false,"valid" to false) else check()
        "login" -> login()
        "cleanup" -> {
            val dir=r.getString("directory")
            if(platform=="xunlei") { require(dir.startsWith("xunlei-file:"));check(xunlei.batchDelete(listOf(dir.removePrefix("xunlei-file:")),token,device,captcha)) { "Cleanup failed" } }
            else { val repository=repo() ?: error("Unknown cleanup platform");repository.cleanupTempDir(dir,credential) }
            obj("ok" to true)
        }
        else -> error("Unknown operation")
    }
    private fun videoItems(v: VideoParseResult): JSONObject {
        check(v.medias.isNotEmpty()) { "No media returned" }
        var items=v.medias.mapIndexed { i,m ->
            val name=m.fileName.ifBlank { "${v.platform.name.lowercase()}-${i+1}." + if(m.kind==VideoMediaKind.IMAGE) "jpg" else "mp4" }
            obj("id" to "media-$i","name" to name,"size" to m.size,"directory" to false,
                "extra" to obj("media_index" to i.toString(),"media_name" to name,"media_kind" to m.kind.name))
        }
        if(v.parts.size>1) items=v.parts.mapIndexed { i,part -> obj("id" to "media-$i","name" to part.fileName,"size" to -1,"directory" to false,"extra" to obj("media_index" to "0","media_name" to part.fileName,"media_kind" to "VIDEO","media_page" to part.page.toString())) }
        return obj("title" to v.title,"items" to JSONArray(items),"more" to false,"notice" to v.notice,
            "qualities" to JSONArray(v.qualities.map { obj("id" to it.qn,"name" to it.label) }),"quality" to v.selectedQn)
    }
    private suspend fun videoSources(): JSONObject {
        val item=r.getJSONObject("item");val extra=item.getJSONObject("extra");val v=videoResult(extra.optString("media_page").toIntOrNull() ?: 0)
        val m=v.medias.getOrNull(extra.getString("media_index").toInt()) ?: error("Media no longer exists")
        check(m.kind.name==extra.getString("media_kind") && (m.fileName.isBlank() || m.fileName==extra.getString("media_name"))) { "Media changed" }
        val sources=JSONArray().put(obj("url" to m.url,"size" to m.size,"headers" to JSONObject(m.headers),"kind" to if(m.isHls) "hls" else "http"))
        if(m.motionUrl.isNotBlank()) sources.put(obj("url" to m.motionUrl,"size" to -1,"headers" to JSONObject(m.headers),"kind" to "motion"))
        else if(m.audioUrl.isNotBlank()) sources.put(obj("url" to m.audioUrl,"size" to -1,"headers" to JSONObject(m.audioHeaders),"kind" to "http"))
        return obj("sources" to sources,"motion_url" to m.motionUrl)
    }
    private suspend fun list(): JSONObject {
        require(page in 1..200)
        var more=false;var cursor=""
        val files: List<ShareFile> = if(cloud) when(platform) {
            "quark" -> (quark.listCloudFiles(parent,cookie,page,50) ?: error("Missing directory response")).also { more=it.isNotEmpty() }
            "uc" -> (uc.listCloudFiles(parent,cookie,page,100) ?: error("Missing directory response")).also { more=it.isNotEmpty() }
            "pan123" -> pan123.listCloudFiles(parent,token,r.optString("cursor").ifBlank { "0" },page).let { cursor=it.second.orEmpty();more=cursor.isNotBlank() && cursor!="-1";it.first }
            "baidu" -> baidu.listCloudFiles(if(parent=="0") "/" else parent,cookie,page).also { more=it.size==100 }.map { if(it.isdir) it.copy(fid=it.fidToken) else it }
            "c139" -> c139.listCloudFiles(if(parent=="0") "" else parent,cookie,r.optString("cursor").takeIf { it.isNotEmpty() }).let { cursor=it.second.orEmpty();more=cursor.isNotEmpty();it.first }
            "pan115" -> pan115.listFiles(parent,cookie,(page-1)*100,100).let { more=it.files.size==100 && page*100<it.total;it.files }
            "guangya" -> guangya.listCloudFiles(token,if(parent=="0") "" else parent)
            "lanzou" -> { val login=lanzou.fetchLoginParams(cookie) ?: error("Login expired");lanzou.listCloudFiles(cookie,login.uid,login.vei,if(parent=="0") "-1" else parent.removePrefix("d:")) }
            "ilanzou" -> ilanzou.listFiles(token,deviceUUID(),parent.removePrefix("d:"))
            "xunlei" -> (xunlei.getFilesPage(if(parent=="0") "" else parent,token,device,captcha,r.optString("cursor")) ?: error("Missing directory response")).let { cursor=it.second;more=cursor.isNotBlank();it.first }
            else -> error("Not a cloud platform")
        } else if(platform=="ilanzou") {
            ilanzou.shareList(parsed().shareId,parent.removePrefix("d:").takeIf { it!="0" && it.isNotBlank() },deviceUUID())
        } else if(platform=="xunlei") {
            val p=parsed();val password=r.optString("password").ifBlank { p.pwd.orEmpty() }
            val share=xunlei.getShare(p.shareId,password,token,device,captcha,if(parent=="0") r.optString("cursor") else "") ?: error("Missing share response")
            if(parent=="0") { cursor=share.nextPageToken;more=cursor.isNotBlank();share.files } else {
                val dir=xunlei.getShareDetail(p.shareId,parent,share.passCodeToken,token,device,captcha,r.optString("cursor")) ?: error("Missing directory response")
                cursor=dir.nextPageToken;more=cursor.isNotBlank();dir.files
            }
        } else { val repo=repo() ?: error("Not a share platform");repo.listFiles(share(repo),if(parent=="0") when(platform){"baidu" -> "/";"guangya","lanzou" -> "";else -> parent} else parent,credential).getOrThrow() }
        check(files.size<=20000) { "Directory exceeds limit" }
        return obj("items" to JSONArray(files.map { it.item() }),"more" to more,"cursor" to cursor)
    }
    private suspend fun sources(): JSONObject {
        val f=file(JSONObject(r.getJSONObject("item").getJSONObject("extra").getString("provider_file")))
        check(!f.isdir)
        val link: DownloadLink = if(cloud) when(platform) {
            "quark" -> QuarkResolveRepository(quark).getDownloadLink(f.fid,cookie).getOrThrow()
            "uc" -> UCResolveRepository(uc).getDownloadLink(f.fid,cookie).getOrThrow()
            "pan123" -> pan123.getDownloadLink(f,token) ?: error("Missing download link")
            "baidu" -> DownloadLink(f.fid,f.fname,baidu.fileMetasDlink(f.fid,cookie),f.fsize)
            "c139" -> c139.getDownloadUrl(f.fid,cookie) ?: error("Missing download link")
            "pan115" -> pan115.getDownloadLink(f,cookie) ?: error("Missing download link")
            "guangya" -> guangya.getDownloadLink(token,f) ?: error("Missing download link")
            "lanzou" -> { val login=lanzou.fetchLoginParams(cookie) ?: error("Login expired");lanzou.getPersonalDownloadLink(cookie,login.uid,login.vei,f.fid,f.fname) }
            "ilanzou" -> ilanzou.getDownloadLink(token,deviceUUID(),f.fid.removePrefix("f:"),c.optString("user_id")) ?: error("Missing download link")
            "xunlei" -> xunlei.getFileDetail(f.fid,token,device,captcha) ?: error("Missing download link")
            else -> error("Unsupported platform")
        } else when(platform) {
            "ilanzou" -> ilanzou.shareDownload(parsed().shareId,f.fid.removePrefix("f:"),c.optString("user_id"),deviceUUID())
            "xunlei" -> {
                val p=parsed();val share=xunlei.getShare(p.shareId,r.optString("password").ifBlank { p.pwd.orEmpty() },token,device,captcha) ?: error("Missing share")
                check(token.isNotBlank()) { "Account required" }
                TransferSpaceGuard.ensureEnoughSpace(f.fsize,"迅雷") { xunlei.getQuota(token,device,captcha) }
                val dir=xunlei.ensureTempDir(token,device,captcha) ?: error("Temporary directory failed")
                val fid=xunlei.restore(p.shareId,share.passCodeToken,dir,listOf(f.fid),token,device,captcha) ?: error("Transfer failed")
                val result=xunlei.getFileDetail(fid,token,device,captcha) ?: error("Missing download link")
                result.copy(cleanupDirFid="xunlei-file:"+fid)
            }
            else -> { val repo=repo() ?: error("Unsupported platform");if(credential.isBlank() && platform in setOf("quark","uc","lanzou","guangya")) repo.getGuestShareDownloadLink(share(repo),f).getOrThrow() else repo.getShareDownloadLink(share(repo),f,credential).getOrThrow() }
        }
        val headers=mutableMapOf("User-Agent" to VideoHttp.UA_CHROME)
        when(platform) {
            "quark" -> { headers["User-Agent"]=QuarkConstants.USER_AGENT;headers["Referer"]="https://pan.quark.cn/" }
            "uc" -> headers["Referer"]="https://drive.uc.cn/"
            "baidu" -> headers["User-Agent"]=BaiduConstants.UA_NETDISK
            "pan115" -> headers["User-Agent"]=Pan115Constants.CLIENT_UA
        }
        val cdnCookie=link.guestCookie.ifBlank { cookie }
        if(cdnCookie.isNotBlank()) headers["Cookie"]=cdnCookie
        return obj("sources" to JSONArray().put(obj("url" to link.downloadUrl,"size" to link.size,"headers" to JSONObject(headers),
            "kind" to if(link.isHls) "hls" else "http","cleanup" to link.cleanupDirFid)))
    }
    private suspend fun check(): JSONObject {
        val name: String? = when(platform) {
            "quark" -> quark.fetchNickname(cookie)
            "uc" -> uc.fetchNickname(cookie)
            "pan123" -> pan123.fetchNickname(token)
            "baidu" -> baidu.fetchNickname(cookie)
            "pan115" -> pan115.fetchNickname(cookie)
            "c139" -> if(c139.getQuota(cookie)!=null) "" else null
            "lanzou" -> lanzou.fetchLoginParams(cookie)?.uid
            "ilanzou" -> if(ilanzou.getQuota(token,deviceUUID())!=null) "" else null
            "guangya" -> if(guangya.getQuota(token,GuangYaDevice(device,c.optString("device_sign").ifBlank { GuangYaConstants.newDeviceSign(device) }))!=null) "" else null
            "xunlei" -> if(xunlei.verifyAccessToken(token,device,captcha)) "" else null
            else -> video?.let { VideoParsers.of(it)?.validateCookie(cookie) }
        }
        return obj("known" to true,"valid" to (name!=null),"nickname" to name.orEmpty())
    }
    private suspend fun login(): JSONObject {
        val name=c.getString("username");val password=c.getString("password")
        return when(platform) {
            "lanzou" -> obj("credentials" to obj("cookie" to lanzou.login(name,password)))
            "ilanzou" -> obj("credentials" to obj("token" to ilanzou.login(name,password,deviceUUID()),"uuid" to deviceUUID()))
            "guangya" -> { val t=guangya.signIn(name,password,GuangYaDevice(device,c.optString("device_sign").ifBlank { GuangYaConstants.newDeviceSign(device) }));obj("credentials" to obj("token" to t.accessToken,"refresh_token" to t.refreshToken,"device_id" to device,"device_sign" to c.optString("device_sign"))) }
            "xunlei" -> { val step=xunlei.loginWithPassword(name,password,device,c.optString("verification_code"));obj("step" to JSONObject(step)) }
            else -> error("Use browser credentials for this platform")
        }
    }
}
