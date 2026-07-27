<?php
// Public LLM & Agent Search / Content Retrieval Endpoint (TOON & JSON Format)
require_once __DIR__ . '/lib.php';

$q = trim($_GET['q'] ?? $_POST['q'] ?? '');
$skip = isset($_GET['skip']) ? max(0, (int)$_GET['skip']) : (isset($_POST['skip']) ? max(0, (int)$_POST['skip']) : 0);

// Default limit is 20. If explicitly set to 0, returns all cards without limit.
if (isset($_GET['limit'])) {
    $limit = (int)$_GET['limit'];
} else if (isset($_POST['limit'])) {
    $limit = (int)$_POST['limit'];
} else {
    $limit = 20;
}

$lang = strtolower(trim($_GET['lang'] ?? $_POST['lang'] ?? 'en'));
$lang = in_array($lang, ['de', 'en']) ? $lang : 'en';

$format = strtolower(trim($_GET['format'] ?? $_POST['format'] ?? 'snippet'));
if (!in_array($format, ['min', 'snippet', 'summary', 'full'])) {
    $format = 'snippet';
}

$crop = isset($_GET['crop']) ? (int)$_GET['crop'] : (isset($_POST['crop']) ? (int)$_POST['crop'] : 0);

$output_mode = strtolower(trim($_GET['output'] ?? $_POST['output'] ?? 'toon'));
$want_json = ($output_mode === 'json') || !empty($_GET['json']) || !empty($_POST['json']);

try {
    // Perform language-resolved vector search or curated list query
    $tiles = search_tiles($lang, $q, $skip, $limit);
    
    $items = [];
    $contents_dir = __DIR__ . '/contents/';

    foreach ($tiles as $idx => $tile) {
        $name = $tile['name'];
        $tile_lang = $tile['language'];
        
        // Clean category tags
        $raw_tags = is_array($tile['category_tags']) ? $tile['category_tags'] : explode(',', trim((string)$tile['category_tags'], '{}'));
        $tags_clean = implode(', ', array_filter(array_map('trim', $raw_tags)));

        // Score calculation: Similarity score is ONLY included when search query q is non-empty
        $has_score = false;
        $score = null;
        if (!empty($q) && isset($tile['distance'])) {
            $dist = (float)$tile['distance'];
            $sim = max(0.0, min(1.0, 1.0 - $dist));
            $score = sprintf('%.2f', $sim);
            $has_score = true;
        }

        // Determine type: 'doc(size)' or 'link' (link has no size)
        $type_str = 'doc';
        if ($tile['reference_type'] === 'link' || (!empty($tile['link']) && empty($tile['content_file']))) {
            $type_str = "link";
        } else if (!empty($tile['content_file'])) {
            $fpath = $contents_dir . $tile['content_file'];
            $size_bytes = file_exists($fpath) ? filesize($fpath) : strlen($tile['html_content']);
            $type_str = "doc({$size_bytes}b)";
        } else {
            $size_bytes = strlen($tile['html_content']);
            $type_str = "doc({$size_bytes}b)";
        }

        $date_str = date('Y-m-d', strtotime($tile['updated_at'] ?? $tile['created_at']));
        $summary = $tile['high_level_summary'] ?? '';

        // Prepare content resolution for 'full'
        $full_body = '';
        if ($format === 'full') {
            if (!empty($tile['content_file']) && file_exists($contents_dir . $tile['content_file'])) {
                $full_body = file_get_contents($contents_dir . $tile['content_file']);
            } else if (!empty($tile['link'])) {
                $full_body = "Link URL: " . $tile['link'];
            } else {
                $full_body = $tile['html_content'];
            }
        }

        // Apply cropping if requested
        if ($format === 'snippet') {
            $max_len = ($crop > 0) ? $crop : 70;
            $display_summary = (mb_strlen($summary) > $max_len) ? mb_substr($summary, 0, $max_len) . '...' : $summary;
        } else if ($crop > 0) {
            $display_summary = (mb_strlen($summary) > $crop) ? mb_substr($summary, 0, $crop) . '...' : $summary;
            if ($full_body) {
                $full_body = (mb_strlen($full_body) > $crop) ? mb_substr($full_body, 0, $crop) . '...' : $full_body;
            }
        } else {
            $display_summary = $summary;
        }

        $item_data = [
            'index' => $skip + $idx + 1,
            'name' => $name,
            'title' => $tile['title'],
            'lang' => $tile_lang,
            'tags' => $tags_clean,
            'type' => $type_str,
            'date' => $date_str,
            'summary' => $display_summary
        ];

        if ($has_score) {
            $item_data['score'] = $score;
        }
        if ($format === 'full' && !empty($full_body)) {
            $item_data['content'] = $full_body;
        }
        if (!empty($tile['link'])) {
            $item_data['link'] = $tile['link'];
        }

        $items[] = $item_data;
    }

    if ($want_json) {
        header('Content-Type: application/json; charset=utf-8');
        echo json_encode([
            'status' => 'success',
            'query' => $q,
            'lang' => $lang,
            'format' => $format,
            'skip' => $skip,
            'limit' => $limit,
            'count' => count($items),
            'data' => $items
        ], JSON_PRETTY_PRINT | JSON_UNESCAPED_UNICODE | JSON_UNESCAPED_SLASHES);
        exit;
    }

    // TOON (Token-Oriented Object Notation) Output
    header('Content-Type: text/plain; charset=utf-8');
    echo "# Leben App LLM Search Results\n";
    echo "# Query: " . ($q ? "'{$q}'" : "[Curated List]") . " | Lang: {$lang} | Format: {$format} | Total: " . count($items) . "\n\n";

    foreach ($items as $item) {
        if (isset($item['score'])) {
            echo "--- CARD {$item['index']} (score: {$item['score']}) ---\n";
            echo "name: {$item['name']} | lang: {$item['lang']} | type: {$item['type']} | score: {$item['score']} | date: {$item['date']}\n";
        } else {
            echo "--- CARD {$item['index']} ---\n";
            echo "name: {$item['name']} | lang: {$item['lang']} | type: {$item['type']} | date: {$item['date']}\n";
        }
        echo "title: {$item['title']}\n";
        echo "tags: {$item['tags']}\n";
        
        if ($format !== 'min') {
            echo "summary: {$item['summary']}\n";
        }
        if ($format === 'full' && !empty($item['content'])) {
            echo "body:\n" . trim($item['content']) . "\n";
        }
        echo "\n";
    }

} catch (Exception $e) {
    if ($want_json) {
        header('Content-Type: application/json; charset=utf-8');
        http_response_code(500);
        echo json_encode(['status' => 'error', 'message' => $e->getMessage()]);
    } else {
        header('Content-Type: text/plain; charset=utf-8');
        http_response_code(500);
        echo "ERROR: " . $e->getMessage();
    }
}
