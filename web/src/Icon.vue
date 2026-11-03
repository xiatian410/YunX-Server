<script setup lang="ts">
import '@mdui/icons/download--outlined.js';
import '@mdui/icons/link--outlined.js';
import '@mdui/icons/cloud--outlined.js';
import '@mdui/icons/settings--outlined.js';
import '@mdui/icons/add.js';
import '@mdui/icons/refresh.js';
import '@mdui/icons/logout.js';
import '@mdui/icons/folder--outlined.js';
import '@mdui/icons/description--outlined.js';
import '@mdui/icons/pause.js';
import '@mdui/icons/play-arrow.js';
import '@mdui/icons/more-horiz.js';
import '@mdui/icons/check.js';
import '@mdui/icons/close.js';
import '@mdui/icons/arrow-back.js';
import '@mdui/icons/error-outline.js';
import '@mdui/icons/storage.js';
import '@mdui/icons/shield--outlined.js';
defineProps<{name:string}>();
</script>
<template><component :is="'mdui-icon-'+name" aria-hidden="true" class="app-icon" /></template>
